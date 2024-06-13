package k8s

import (
	"context"
	"fmt"
	"log"
	"net/url"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type FunctionLookupRemote struct {
	DefaultNamespace string
	ExternalGateway  string
}

func NewFunctionLookupRemote(clientset *kubernetes.Clientset) *FunctionLookupRemote {
	return &FunctionLookupRemote{
		ExternalGateway: exploreExternalGateway(clientset),
	}
}

func (l *FunctionLookupRemote) Resolve(name string) (url.URL, error) {
	functionAddr := url.URL{
		Scheme: "http",
		Host:   l.ExternalGateway + ":80",
	}
	// offload.buildProxyRequest
	// proxyReq, err := proxy.buildProxyRequest(originalReq, functionAddr, pathVars["params"])

	return functionAddr, nil
}

func exploreExternalGateway(clientset *kubernetes.Clientset) string {
	serviceNamespace := "openfaas"
	ingresses, err := clientset.NetworkingV1().Ingresses(serviceNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Println(err.Error())
		return ""
	}

	if items := ingresses.Items; len(items) > 0 {
		if rules := items[0].Spec.Rules; len(rules) > 0 {
			return rules[0].Host
		} else {
			log.Println("No ingress item found")
		}
	} else {
		log.Println("No ingress item found")
	}

	return ""
}

// func clusterResourcePressure() {
// 	//use endpoint /metrics to query the cluster pressure periodically
// }
