package k8s

import (
	"context"
	"fmt"
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
	}
	// for _, ingress := range ingresses.Items {
	// 	fmt.Printf(" * %s, %s\n", ingress.Spec.Rules[0].Host, ingress.Status.LoadBalancer.Ingress[0].IP)
	// }
	return ingresses.Items[0].Spec.Rules[0].Host
}
