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

// use the k8s api server ip, expect the openfaas service is nodeport service
func NewFunctionLookupRemote(ip string) *FunctionLookupRemote {
	return &FunctionLookupRemote{
		ExternalGateway: ip,
		// ExternalGateway: exploreExternalGateway(clientset),
	}
}

func (l FunctionLookupRemote) Resolve(name string) (url.URL, error) {
	functionAddr := url.URL{
		Scheme: "http",
		// might be 31112?
		Host: l.ExternalGateway + ":31112",
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
