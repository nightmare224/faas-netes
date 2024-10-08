package catalog

import (
	"context"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	clientset "github.com/openfaas/faas-netes/pkg/client/clientset/versioned"
	faasinformers "github.com/openfaas/faas-netes/pkg/client/informers/externalversions"
	v1 "github.com/openfaas/faas-netes/pkg/client/informers/externalversions/openfaas/v1"
	"github.com/openfaas/faas-netes/pkg/config"
	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/proxy"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	v1apps "k8s.io/client-go/informers/apps/v1"
	v1core "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	v1appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const defaultResync = time.Hour * 10

type NewKubeClientWithIpFunc func(ip string, p2pID string) KubeClient
type KubeClient struct {
	Clientset      *kubernetes.Clientset
	Factory        k8s.FunctionFactory
	DeployLister   v1appslisters.DeploymentLister
	InvokeResolver proxy.BaseURLResolver
	Informers      customInformers
	// for convenient
	P2PID string
}

func NewKubeConfig(kubeconfigPath string) ([]*rest.Config, error) {
	var clientCmdConfigs []*rest.Config
	// prevent duplicate kubeconfig
	clusterIDSet := make(map[string]struct{})

	//loading local
	config, configErr := rest.InClusterConfig()
	if configErr != nil {
		log.Fatalf("Error building kubeconfig for local: %s", configErr.Error())
	} else {
		clientCmdConfigs = append(clientCmdConfigs, config)
		clusterID := getClusterIdentifier(config)
		clusterIDSet[clusterID] = struct{}{}
	}

	err := filepath.WalkDir(kubeconfigPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := os.Lstat(path)
		if (fi.Mode()&fs.ModeSymlink == 0) && !d.IsDir() {
			config, err := clientcmd.BuildConfigFromFlags("", path)
			if err != nil {
				log.Fatalf("Error building kubeconfig: %s", err.Error())
			}
			clusterID := getClusterIdentifier(config)
			if _, exists := clusterIDSet[clusterID]; !exists {
				clusterIDSet[clusterID] = struct{}{}
				clientCmdConfigs = append(clientCmdConfigs, config)
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Error building kubeconfig path: %s", err.Error())
		return nil, err
	}
	return clientCmdConfigs, nil
}

func NewKubeClientWithIpGenerator(config config.BootstrapConfig, clientCmdConfigMap map[string]*rest.Config, stopCh <-chan struct{}, operator bool) NewKubeClientWithIpFunc {
	deployConfig := k8s.DeploymentConfig{
		RuntimeHTTPPort: 8080,
		HTTPProbe:       config.HTTPProbe,
		SetNonRootUser:  config.SetNonRootUser,
		ReadinessProbe: &k8s.ProbeConfig{
			InitialDelaySeconds: int32(2),
			TimeoutSeconds:      int32(1),
			PeriodSeconds:       int32(2),
		},
		LivenessProbe: &k8s.ProbeConfig{
			InitialDelaySeconds: int32(2),
			TimeoutSeconds:      int32(1),
			PeriodSeconds:       int32(2),
		},
	}
	return func(ip string, p2pID string) KubeClient {
		clientCmdConfig, exist := clientCmdConfigMap[ip]
		if !exist {
			log.Printf("Cannot find the kubeclient config of ip %s\n", ip)
			// the one cannot find always the remote one
			return KubeClient{P2PID: p2pID, InvokeResolver: k8s.NewFunctionLookupRemote(ip)}
		}
		kubeClientset := newKubeClientset(clientCmdConfig)
		faasClientset := newFaasClientset(clientCmdConfig)
		kubeInformerOpt := kubeinformers.WithNamespace(config.DefaultFunctionNamespace)
		kubeInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClientset, defaultResync, kubeInformerOpt)
		faasInformerOpt := faasinformers.WithNamespace(config.DefaultFunctionNamespace)
		faasInformerFactory := faasinformers.NewSharedInformerFactoryWithOptions(faasClientset, defaultResync, faasInformerOpt)
		informers := startInformers(kubeInformerFactory, faasInformerFactory, stopCh, operator)

		// create deploy lister
		deployLister := informers.DeploymentInformer.Lister()

		// create function resolver, assume the index 0 is the local one
		var invokeResolver proxy.BaseURLResolver
		if ip == GetSelfFaasP2PIp() {
			invokeResolver = k8s.NewFunctionLookup(config.DefaultFunctionNamespace, informers.EndpointsInformer.Lister())
		} else {
			invokeResolver = k8s.NewFunctionLookupRemote(ip)
		}

		return KubeClient{
			Clientset:      kubeClientset,
			Factory:        k8s.NewFunctionFactory(kubeClientset, deployConfig, faasClientset.OpenfaasV1()),
			DeployLister:   deployLister,
			Informers:      informers,
			InvokeResolver: invokeResolver,
			// fill in later
			P2PID: p2pID,
		}
	}
}

func newKubeClientset(clientCmdConfig *rest.Config) *kubernetes.Clientset {
	kubeconfigQPS := 100
	kubeconfigBurst := 250
	clientCmdConfig.QPS = float32(kubeconfigQPS)
	clientCmdConfig.Burst = kubeconfigBurst
	kubeClient, err := kubernetes.NewForConfig(clientCmdConfig)
	if err != nil {
		log.Fatalf("Error building Kubernetes clientset: %s", err.Error())
	}

	return kubeClient
}
func newFaasClientset(clientCmdConfig *rest.Config) *clientset.Clientset {
	faasClient, err := clientset.NewForConfig(clientCmdConfig)
	if err != nil {
		log.Fatalf("Error building OpenFaaS clientset: %s", err.Error())
	}
	return faasClient
}

// sort the P2P ID from the fastest to slowest
func (c Catalog) RankNodeByRTT() {

	// TODO: make this run periodically?
	RTTs := make([]time.Duration, 0)
	RTTtoP2PID := make(map[time.Duration]string)
	for p2pID, p2pNode := range c.NodeCatalog {
		// for itself it is 0
		rtt := time.Duration(0)
		if p2pID != selfCatagoryKey {
			startTime := time.Now()
			// the path of faas gateway
			url, _ := p2pNode.InvokeResolver.Resolve("")
			ip, _, _ := net.SplitHostPort(url.Host)
			// just try connect with the k8s api
			conn, err := net.DialTimeout("tcp", ip+":22", 5*time.Second)
			rtt = time.Since(startTime)
			if err != nil {
				log.Printf("Measure RTT TCP connection error: %s, just continue", err.Error())
			} else {
				conn.Close()
			}
		}
		RTTtoP2PID[rtt] = p2pID
		RTTs = append(RTTs, rtt)
	}
	slices.Sort(RTTs)

	// make the length fit with the number of node
	*c.SortedP2PID = (*c.SortedP2PID)[:len(RTTs)]
	// copy back to original array
	for i, rtt := range RTTs {
		(*c.SortedP2PID)[i] = RTTtoP2PID[rtt]
	}
}

type customInformers struct {
	EndpointsInformer  v1core.EndpointsInformer
	DeploymentInformer v1apps.DeploymentInformer
	FunctionsInformer  v1.FunctionInformer
}

func startInformers(kubeInformerFactory kubeinformers.SharedInformerFactory, faasInformerFactory faasinformers.SharedInformerFactory, stopCh <-chan struct{}, operator bool) customInformers {

	var functions v1.FunctionInformer
	if operator {
		functions = faasInformerFactory.Openfaas().V1().Functions()
		go functions.Informer().Run(stopCh)
		if ok := cache.WaitForNamedCacheSync("faas-netes:functions", stopCh, functions.Informer().HasSynced); !ok {
			log.Fatalf("failed to wait for cache to sync")
		}
	}
	// start the informer for Kubernetes deployments in a new goroutine,
	//and listen for events related to deployments until a stop signal is received through the stopCh channel
	deployments := kubeInformerFactory.Apps().V1().Deployments()
	go deployments.Informer().Run(stopCh)
	if ok := cache.WaitForNamedCacheSync("faas-netes:deployments", stopCh, deployments.Informer().HasSynced); !ok {
		log.Fatalf("failed to wait for cache to sync")
	}

	endpoints := kubeInformerFactory.Core().V1().Endpoints()
	go endpoints.Informer().Run(stopCh)
	if ok := cache.WaitForNamedCacheSync("faas-netes:endpoints", stopCh, endpoints.Informer().HasSynced); !ok {
		log.Fatalf("failed to wait for cache to sync")
	}

	return customInformers{
		EndpointsInformer:  endpoints,
		DeploymentInformer: deployments,
		FunctionsInformer:  functions,
	}
}

// user openfaas namespace uuid as cluster id
func getClusterIdentifier(config *rest.Config) string {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error building Kubernetes clientset: %s", err.Error())
	}
	namespace, err := clientset.CoreV1().Namespaces().Get(context.TODO(), "openfaas", metav1.GetOptions{})
	if err != nil {
		log.Fatalf("Error get openfaas namespace: %s", err.Error())
	}
	return string(namespace.UID)
}
