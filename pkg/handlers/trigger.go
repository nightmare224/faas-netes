package handlers

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/proxy"
	types "github.com/openfaas/faas-provider/types"
	v1 "k8s.io/client-go/listers/apps/v1"
)

// Make this able to search for other cluster's function.
// And if other cluster exist the function, them deploy it on current local one
// and trigger on local cluster
func MakeTriggerHandler(functionNamespace string, config types.FaaSConfig, resolvers []proxy.BaseURLResolver, verbose bool, deploymentListers []v1.DeploymentLister, factory k8s.FunctionFactory) http.HandlerFunc {

	// MakeReplicaReader(functionNamespace, deploymentListers[0])
	// secrets := k8s.NewSecretsClient(factory.Client)
	resolver := resolvers[0]
	return func(w http.ResponseWriter, r *http.Request) {
		// multi cluster scenario
		if len(deploymentListers) > 0 {
			vars := mux.Vars(r)
			functionName := vars["name"]
			for i, lister := range deploymentListers {
				function, _ := getService(functionNamespace, functionName, lister)
				if function != nil {
					// function already in local
					if i == 0 {
						fmt.Printf("The function %s is already deployed in the targeted cluster.\n", functionName)
						break
					} else {
						fmt.Printf("The function %s is found in cluster %d.\n", functionName, i)
					}
					deployment := types.FunctionDeployment{
						Service:                function.Name,
						Image:                  function.Image,
						Namespace:              function.Namespace,
						EnvProcess:             function.EnvProcess,
						EnvVars:                function.EnvVars,
						Constraints:            function.Constraints,
						Secrets:                function.Constraints,
						Labels:                 function.Labels,
						Annotations:            function.Annotations,
						Limits:                 function.Limits,
						Requests:               function.Requests,
						ReadOnlyRootFilesystem: function.ReadOnlyRootFilesystem,
					}
					functionList := k8s.NewFunctionList(functionNamespace, lister)
					err, httpStatusCode := makeFunction(functionNamespace, factory, functionList, deployment)
					if err != nil {
						http.Error(w, err.Error(), httpStatusCode)
						return
					}
					break
				}
			}
		}

		// The original handler, still only on local
		proxy.NewHandlerFunc(config, resolver, verbose)(w, r)
	}

}
