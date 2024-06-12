package catalog

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/proxy"
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

func NewKubeP2PMappingList(ipKubeMapping map[string]KubeP2PMapping, c Catalog) KubeP2PMappingList {

	kubeP2PMappingList := KubeP2PMappingList{
		// also add itself into it (for itself, it don't need the faas client)

	}
	// //
	// faasClients := newFaasClients(c.NodeCatalog[selfCatagoryKey].Ip)
	// for _, client := range faasClients {
	// 	for p2pID, p2pNode := range c.NodeCatalog {
	// 		if strings.HasPrefix(client.GatewayURL.Host, p2pNode.Ip) {
	// 			mapping := FaasP2PMapping{
	// 				FaasClient: client,
	// 				P2PID:      p2pID,
	// 			}
	// 			faasP2PMappingList = append(faasP2PMappingList, mapping)
	// 			break
	// 		}
	// 	}

	// 	// testFaasClient(client)
	// }
	return kubeP2PMappingList
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
