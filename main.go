// Copyright (c) Alex Ellis 2017. All rights reserved.
// Copyright (c) OpenFaaS Author(s) 2020. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"slices"
	"sync/atomic"
	"time"

	"github.com/openfaas/faas-netes/pkg/catalog"
	clientset "github.com/openfaas/faas-netes/pkg/client/clientset/versioned"
	informers "github.com/openfaas/faas-netes/pkg/client/informers/externalversions"
	v1 "github.com/openfaas/faas-netes/pkg/client/informers/externalversions/openfaas/v1"
	"github.com/openfaas/faas-netes/pkg/config"
	"github.com/openfaas/faas-netes/pkg/handlers"
	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-netes/pkg/signals"
	version "github.com/openfaas/faas-netes/version"
	faasProvider "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/logs"
	"github.com/openfaas/faas-provider/proxy"
	providertypes "github.com/openfaas/faas-provider/types"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	kubeinformers "k8s.io/client-go/informers"
	v1apps "k8s.io/client-go/informers/apps/v1"
	v1core "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	v1appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	// required to authenticate against GKE clusters
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	// required for updating and validating the CRD clientset
	_ "k8s.io/code-generator/cmd/client-gen/generators"
	// main.go:36:2: import "sigs.k8s.io/controller-tools/cmd/controller-gen" is a program, not an importable package
	// _ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)

const defaultResync = time.Hour * 10
const selfSetupIP = "0.0.0.0"

func main() {
	var kubeconfig string
	var masterURL string
	var (
		verbose bool
	)
	var kubeconfigPath string

	flag.StringVar(&kubeconfig, "kubeconfig", "",
		"Path to a kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&verbose, "verbose", false, "Print verbose config information")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.Bool("operator", false, "Run as an operator (not available in CE)")
	flag.StringVar(&kubeconfigPath, "kubeconfigpath", "",
		"Path to a kubeconfig directory. Only required if multiple OpenFaaS clusters exist.")

	flag.Parse()

	mode := "controller"

	sha, release := version.GetReleaseInfo()
	fmt.Printf("faas-netes - Community Edition (CE)\n"+
		"Warning: Commercial use limited to 60 days.\n"+
		"\nVersion: %s Commit: %s Mode: %s\n", release, sha, mode)

	if err := config.ConnectivityCheck(); err != nil {
		log.Fatalf("Error checking connectivity, OpenFaaS CE cannot be run in an offline environment: %s", err.Error())
	}

	// TODO: can be bettter
	var clientCmdConfigs []*rest.Config
	// clusterIDSet := make(map[string]struct{})
	if kubeconfigPath != "" {
		configs, err := catalog.NewKubeConfig(kubeconfigPath)
		if err != nil {
			log.Fatalf("Error building kubeconfig path: %s", err.Error())
			panic(err)
		}
		clientCmdConfigs = append(clientCmdConfigs, configs...)
	} else {
		config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
		if err != nil {
			log.Fatalf("Error building kubeconfig: %s", err.Error())
		}
		clientCmdConfigs = []*rest.Config{config}
	}

	fmt.Println("ClientCmdConfigs len:", len(clientCmdConfigs))

	// set config
	readConfig := config.ReadConfig{}
	osEnv := providertypes.OsEnv{}
	config, err := readConfig.Read(osEnv)
	if err != nil {
		log.Fatalf("Error reading config: %s", err.Error())
	}
	config.Fprint(verbose)
	if config.DefaultFunctionNamespace == "" {
		klog.Fatal("DefaultFunctionNamespace must be set")
	}

	// setups
	kubeconfigQPS := 100
	kubeconfigBurst := 250
	setups := make(map[string]serverSetup, len(clientCmdConfigs))
	// for i := 0; i < numConfig; i++ {
	for i, clientCmdConfig := range clientCmdConfigs {
		// create kubeClients
		clientCmdConfig.QPS = float32(kubeconfigQPS)
		clientCmdConfig.Burst = kubeconfigBurst
		kubeClient, err := kubernetes.NewForConfig(clientCmdConfig)
		if err != nil {
			log.Fatalf("Error building Kubernetes clientset: %s", err.Error())
		}
		//create faasClients
		faasClient, err := clientset.NewForConfig(clientCmdConfig)
		if err != nil {
			log.Fatalf("Error building OpenFaaS clientset: %s", err.Error())
		}
		// store clients into setups
		setup := serverSetup{
			// all the config remain same
			config:     config,
			kubeClient: kubeClient,
			faasClient: faasClient,
		}
		if i == 0 {
			setups[selfSetupIP] = setup
		} else {
			parsedURL, _ := url.Parse(clientCmdConfig.Host)
			ip, _, _ := net.SplitHostPort(parsedURL.Host)
			setups[ip] = setup
		}

	}
	log.Printf("Server setup %v\n", setups)

	runController(setups)
}

type customInformers struct {
	EndpointsInformer  v1core.EndpointsInformer
	DeploymentInformer v1apps.DeploymentInformer
	FunctionsInformer  v1.FunctionInformer
}
type customPlatformInformers struct {
	ServiceInformer v1core.ServiceInformer
}

func measureRTT(clientCmdConfigs []*rest.Config) []*rest.Config {

	var sortedRTTConfig []*rest.Config
	// check if there is local cluster
	kubeconfig, err := rest.InClusterConfig()

	localhost := ""
	if err == nil {
		sortedRTTConfig = append(sortedRTTConfig, kubeconfig)
		localhost = kubeconfig.Host
		fmt.Println("Localhost Cluster IP: ", localhost)
	}
	RTTtoIdx := make(map[time.Duration]int)
	var RTTs []time.Duration
	for idx, config := range clientCmdConfigs {
		// ignore the localhost config as it is already added

		if config.Host != localhost {
			startTime := time.Now()
			parsedURL, _ := url.Parse(config.Host)
			// fmt.Println(config.Host, parsedURL.Host)
			conn, err := net.DialTimeout("tcp", parsedURL.Host, 5*time.Second)
			if err != nil {
				fmt.Printf("Measure RTT TCP connection error: %s", err.Error())
			}
			rtt := time.Since(startTime)
			conn.Close()
			// for testing
			// if config.Host == "https://10.211.55.25:6443" {
			// 	rtt = 0
			// }
			RTTtoIdx[rtt] = idx
			RTTs = append(RTTs, rtt)
			fmt.Println("RTT: ", rtt, "URL: ", config.Host)
		}
	}
	slices.Sort(RTTs)
	for _, rtt := range RTTs {
		sortedRTTConfig = append(sortedRTTConfig, clientCmdConfigs[RTTtoIdx[rtt]])
	}

	return sortedRTTConfig
}
func startInformers(kubeInformerFactory kubeinformers.SharedInformerFactory, faasInformerFactory informers.SharedInformerFactory, stopCh <-chan struct{}, operator bool) customInformers {

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

// the informer for openfaas namespace not openfaas-fn namespace
// func startPlatformInformers(kubeClient *kubernetes.Clientset, stopCh <-chan struct{}) customPlatformInformers {
// 	namespaceScope := "openfaas"
// 	kubeInformerOpt := kubeinformers.WithNamespace(namespaceScope)
// 	kubeInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResync, kubeInformerOpt)

// 	// kubeInformerFactory := setup.kubeInformerFactory
// 	services := kubeInformerFactory.Core().V1().Services()
// 	go services.Informer().Run(stopCh)
// 	if ok := cache.WaitForNamedCacheSync("faas-netes:services", stopCh, services.Informer().HasSynced); !ok {
// 		log.Fatalf("failed to wait for cache to sync")
// 	}

// 	return customPlatformInformers{
// 		ServiceInformer: services,
// 	}
// }

// runController runs the faas-netes imperative controller
func runController(setups map[string]serverSetup) {
	config := setups[selfSetupIP].config
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

	ipKubeMapping := make(map[string]catalog.KubeP2PMapping)
	var functionList *k8s.FunctionList = nil
	operator := false
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()
	for ip, setup := range setups {
		kubeInformerOpt := kubeinformers.WithNamespace(config.DefaultFunctionNamespace)
		kubeInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(setup.kubeClient, defaultResync, kubeInformerOpt)
		faasInformerOpt := informers.WithNamespace(config.DefaultFunctionNamespace)
		faasInformerFactory := informers.NewSharedInformerFactoryWithOptions(setup.faasClient, defaultResync, faasInformerOpt)
		informers := startInformers(kubeInformerFactory, faasInformerFactory, stopCh, operator)
		handlers.RegisterEventHandlers(informers.DeploymentInformer, setup.kubeClient, config.DefaultFunctionNamespace)

		// create deploy lister
		deployLister := informers.DeploymentInformer.Lister()

		// create function resolver, assume the index 0 is the local one
		var invokeResolver proxy.BaseURLResolver
		p2pid := ""
		if ip == selfSetupIP {
			invokeResolver = k8s.NewFunctionLookup(config.DefaultFunctionNamespace, informers.EndpointsInformer.Lister())
			functionList = k8s.NewFunctionList(config.DefaultFunctionNamespace, deployLister)
			// set the self p2p id now
			p2pid = catalog.NewCatalog().GetSelfCatalogKey()
		} else {
			invokeResolver = k8s.NewFunctionLookupRemote(setup.kubeClient)
		}

		ipKubeMapping[ip] = catalog.KubeP2PMapping{
			KubeClient:     setup.kubeClient,
			Factory:        k8s.NewFunctionFactory(setups[ip].kubeClient, deployConfig, setups[ip].faasClient.OpenfaasV1()),
			DeployLister:   deployLister,
			InvokeResolver: invokeResolver,
			// fill in later
			P2PID: p2pid,
		}
	}

	// create the catalog to store p
	c := catalog.NewCatalog()
	node := initSelfCatagory(c, config.DefaultFunctionNamespace, ipKubeMapping[selfSetupIP].DeployLister)
	// create catalog
	InitNetworkErr := catalog.InitInfoNetwork(c)
	if InitNetworkErr != nil {
		err := fmt.Errorf("cannot init info network: %s", InitNetworkErr)
		panic(err)
	}

	// start the local update
	promClient := initPromClient()
	go node.ListenUpdateInfo(&promClient)

	kubeP2PMappingList := catalog.NewKubeP2PMappingList(ipKubeMapping, selfSetupIP, c)
	localKubeP2PMapping := ipKubeMapping[selfSetupIP]

	// create a handle a pass the config into, so the closure can hold the config
	// printFunctionExecutionTime := true
	bootstrapHandlers := providertypes.FaaSHandlers{
		// FunctionProxy:  proxy.NewHandlerFunc(config.FaaSConfig, functionLookupInterfaces, printFunctionExecutionTime),
		FunctionProxy:  handlers.MakeTriggerHandler(config.DefaultFunctionNamespace, config.FaaSConfig, kubeP2PMappingList, c),
		DeleteFunction: handlers.MakeDeleteHandler(config.DefaultFunctionNamespace, localKubeP2PMapping.KubeClient, c),
		// deploy on local cluster (index[0])
		DeployFunction: handlers.MakeDeployHandler(config.DefaultFunctionNamespace, localKubeP2PMapping.Factory, functionList, localKubeP2PMapping.KubeClient, c),
		FunctionLister: handlers.MakeFunctionReader(config.DefaultFunctionNamespace, c),
		FunctionStatus: handlers.MakeReplicaReader(config.DefaultFunctionNamespace, c),
		ScaleFunction:  handlers.MakeReplicaUpdater(config.DefaultFunctionNamespace, localKubeP2PMapping.KubeClient),
		UpdateFunction: handlers.MakeUpdateHandler(config.DefaultFunctionNamespace, localKubeP2PMapping.Factory),
		Health:         handlers.MakeHealthHandler(node),
		Info:           handlers.MakeInfoHandler(version.BuildVersion(), version.GitCommit),
		Secrets:        handlers.MakeSecretHandler(config.DefaultFunctionNamespace, localKubeP2PMapping.KubeClient),
		Logs:           logs.NewLogHandlerFunc(k8s.NewLogRequestor(localKubeP2PMapping.KubeClient, config.DefaultFunctionNamespace), config.FaaSConfig.WriteTimeout),
		ListNamespaces: handlers.MakeNamespacesLister(config.DefaultFunctionNamespace, localKubeP2PMapping.KubeClient),
	}

	ctx := context.Background()

	faasProvider.Serve(ctx, &bootstrapHandlers, &config.FaaSConfig)
}

// serverSetup is a container for the config and clients needed to start the
// faas-netes controller or operator
type serverSetup struct {
	config     config.BootstrapConfig
	kubeClient *kubernetes.Clientset
	faasClient *clientset.Clientset
	// functionFactory     k8s.FunctionFactory
	// kubeInformerFactory kubeinformers.SharedInformerFactory
	// faasInformerFactory informers.SharedInformerFactory
}

func initSelfCatagory(c catalog.Catalog, functionNamespace string, deploymentLister v1appslisters.DeploymentLister) *catalog.Node {
	// init available function to catalog
	fns, err := handlers.ListFunctionStatus(functionNamespace, deploymentLister)
	if err != nil {
		fmt.Printf("cannot init available function: %s", err)
		panic(err)
	}

	node := catalog.NewNode()
	c.NodeCatalog[c.GetSelfCatalogKey()] = &node
	for i, fn := range fns {
		// TODO: should be more sphofisticate
		c.FunctionCatalog[fn.Name] = &fns[i]
		c.NodeCatalog[c.GetSelfCatalogKey()].AvailableFunctionsReplicas[fn.Name] = fn.AvailableReplicas
		c.NodeCatalog[c.GetSelfCatalogKey()].FunctionExecutionTime[fn.Name] = new(atomic.Int64)
		c.NodeCatalog[c.GetSelfCatalogKey()].FunctionExecutionTime[fn.Name].Store(1)
	}

	return c.NodeCatalog[c.GetSelfCatalogKey()]
}

func initPromClient() promv1.API {
	address := "http://prometheus.openfaas.svc:9090"
	promClient, _ := promapi.NewClient(promapi.Config{
		Address: address,
	})

	promAPIClient := promv1.NewAPI(promClient)

	return promAPIClient
}
