package catalog

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/proxy"
	probing "github.com/prometheus-community/pro-bing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	v1appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubeP2PMapping struct {
	KubeClient     *kubernetes.Clientset
	Factory        k8s.FunctionFactory
	DeployLister   v1appslisters.DeploymentLister
	InvokeResolver proxy.BaseURLResolver
	P2PID          string
}
type KubeP2PMappingList []KubeP2PMapping

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
			fmt.Println("cluster ID: ", getClusterIdentifier(config))
			clusterID := getClusterIdentifier(config)
			if _, exists := clusterIDSet[clusterID]; !exists {
				clusterIDSet[clusterID] = struct{}{}
				clientCmdConfigs = append(clientCmdConfigs, config)
				fmt.Printf("Host: %s, APIPath: %s\n", clientCmdConfigs[len(clientCmdConfigs)-1].Host, clientCmdConfigs[len(clientCmdConfigs)-1].APIPath)
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

// assume the p2p ip is same with kubernetes api id, so it can be mapped
func NewKubeP2PMappingList(ipKubeMapping map[string]KubeP2PMapping, selfIp string, c Catalog) KubeP2PMappingList {

	// put itself in to list, which the local one already fill the p2p id
	kubeP2PMappingList := KubeP2PMappingList{ipKubeMapping[selfIp]}
	// TODO: this is work around, to prevent the nodecatalog not yet init
	time.Sleep(5 * time.Second)
	for p2pID, p2pNode := range c.NodeCatalog {
		if p2pID != c.GetSelfCatalogKey() {
			kube, exist := ipKubeMapping[p2pNode.Ip]
			if !exist {
				log.Fatalf("cannot find the corresponding kubernetes client for host: %s\n", p2pNode.Ip)
			}
			kube.P2PID = p2pID
			kubeP2PMappingList = append(kubeP2PMappingList, kube)
		}
	}

	rankClientsByRTT(kubeP2PMappingList)

	return kubeP2PMappingList
}

// put it from the fastest client to slowest client
func rankClientsByRTT(kubeP2PMappingList KubeP2PMappingList) {

	// TODO: make this run periodically?
	var RTTs []time.Duration
	RTTtoMapping := make(map[time.Duration]KubeP2PMapping)
	for _, mapping := range kubeP2PMappingList {
		// for itself it is 0
		rtt := time.Duration(0)
		if mapping.P2PID != selfCatagoryKey {
			// not the local, will resolve the gateway url
			gatewayURL, err := mapping.InvokeResolver.Resolve("")
			if err != nil {
				log.Fatal("failed to resolve gateway URL")
			}
			ip, _, _ := net.SplitHostPort(gatewayURL.Host)
			pinger, err := probing.NewPinger(ip)
			if err != nil {
				panic(err)
			}
			pinger.Count = 3
			err = pinger.Run() // Blocks until finished.
			if err != nil {
				panic(err)
			}
			stats := pinger.Statistics()
			rtt = stats.AvgRtt
		}
		RTTtoMapping[rtt] = mapping
		RTTs = append(RTTs, rtt)
		fmt.Println("RTT: ", rtt, "P2P ID: ", mapping.P2PID)
	}
	slices.Sort(RTTs)
	// copy back to original array
	for i, rtt := range RTTs {
		kubeP2PMappingList[i] = RTTtoMapping[rtt]
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
