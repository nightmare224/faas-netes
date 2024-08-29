package catalog

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/types"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1apps "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// only register for itself (for local cluster, no need to regsiter for remote cluster)
func (c Catalog) RegisterEventHandlers(deploymentInformer v1apps.DeploymentInformer, kubeClient *kubernetes.Clientset, namespace string) {
	node := c.NodeCatalog[GetSelfCatalogKey()]
	deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			deployment, ok := obj.(*appsv1.Deployment)
			if !ok || deployment == nil {
				return
			}
			fname := deployment.Name

			go func() {
				fn, err := WaitDeployReadyAndReport(kubeClient, namespace, fname)
				if err != nil {
					log.Printf("[Deploy] error deploying %s, error: %s\n", fname, err)
					return
				}
				c.AddAvailableFunctions(fn)
			}()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newDeploy, ok := newObj.(*appsv1.Deployment)
			if !ok || newDeploy == nil {
				return
			}
			oldDeploy, ok := oldObj.(*appsv1.Deployment)
			if !ok || oldDeploy == nil {
				return
			}

			replicas, exist := node.AvailableFunctionsReplicas[newDeploy.Name]
			// if replicas is zero should trigger the AddFunc
			if exist && (replicas > 0) {
				/* maybe change it to oldDeploy.Status.Replicas >= 1 ? */
				if (oldDeploy.Status.Replicas >= 1) && (newDeploy.Status.Replicas == 0) {

					newFn := k8s.AsFunctionStatus(*newDeploy)
					newFn.AvailableReplicas, newFn.Replicas = 0, 0
					// only explict delete when send delete api
					c.UpdateAvailableFunctions(*newFn)
				} else if *newDeploy.Spec.Replicas != int32(replicas) {
					// find the replica update event
					newFn := k8s.AsFunctionStatus(*newDeploy)
					// treat it as ready directly here
					newFn.AvailableReplicas = newFn.Replicas
					c.UpdateAvailableFunctions(*newFn)
				}
			}
		},
	})
}

func WaitDeployReadyAndReport(kubeClient *kubernetes.Clientset, functionNamespace string, functionName string) (types.FunctionStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	watch, err := kubeClient.AppsV1().Deployments(functionNamespace).Watch(ctx, metav1.ListOptions{LabelSelector: fmt.Sprintf("faas_function=%s", functionName)})
	if err != nil {
		log.Printf("Unable to watch function: %v", err.Error())
		return types.FunctionStatus{}, err
	}
	for {
		select {
		case event, ok := <-watch.ResultChan():
			if !ok {
				err := fmt.Errorf("deployment watch channel for function %s closed", functionName)
				return types.FunctionStatus{}, err
			}
			dep, ok := event.Object.(*appsv1.Deployment)
			if !ok {
				continue
			}
			if dep.Status.ReadyReplicas >= 1 {
				watch.Stop()
				return *k8s.AsFunctionStatus(*dep), nil
			}
		case <-ctx.Done():
			err := fmt.Errorf("deployment watch channel for function %s closed", functionName)
			watch.Stop()
			return types.FunctionStatus{}, err
		}
	}
}
