package handlers

import (
	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/types"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	v1appslisters "k8s.io/client-go/listers/apps/v1"
)

func ListFunctionStatus(functionNamespace string, deploymentLister v1appslisters.DeploymentLister) ([]types.FunctionStatus, error) {

	functions := []types.FunctionStatus{}

	sel := labels.NewSelector()
	req, err := labels.NewRequirement("faas_function", selection.Exists, []string{})
	if err != nil {
		return functions, err
	}
	onlyFunctions := sel.Add(*req)

	functionnameSet := make(map[string]struct{})
	res, err := deploymentLister.Deployments(functionNamespace).List(onlyFunctions)

	if err != nil {
		return nil, err
	}
	for _, item := range res {
		if item != nil {
			// ignore if the function already is listed by closet cluster
			if _, exist := functionnameSet[item.Name]; exist {
				continue
			}
			function := k8s.AsFunctionStatus(*item)
			if function != nil {
				functions = append(functions, *function)
				functionnameSet[item.Name] = struct{}{}
			}
		}
	}

	return functions, nil
}
