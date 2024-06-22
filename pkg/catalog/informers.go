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
			// only trigger add if it is not yet in record
			if _, exist := node.AvailableFunctionsReplicas[fname]; !exist {
				go func() {
					fn, err := WaitDeployReadyAndReport(kubeClient, namespace, fname)
					if err != nil {
						log.Printf("[Deploy] error deploying %s, error: %s\n", fname, err)
						return
					}
					c.AddAvailableFunctions(fn)
				}()
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			newDeploy, ok := newObj.(*appsv1.Deployment)
			if !ok || newDeploy == nil {
				return
			}
			newFn := k8s.AsFunctionStatus(*newDeploy)
			// only for update available replica
			replicas, exist := node.AvailableFunctionsReplicas[newFn.Name]
			// should be fix
			if exist && replicas != newFn.AvailableReplicas && newFn.AvailableReplicas > 0 {
				c.UpdateAvailableFunctions(*newFn)
			}
		},
		DeleteFunc: func(obj interface{}) {
			log.Printf("Delete deployment added in register event handlers\n")
			deployment, ok := obj.(*appsv1.Deployment)
			if !ok || deployment == nil {
				return
			}
			log.Printf("Delete: %v\n", deployment.Name)
			c.DeleteAvailableFunctions(deployment.Name)
		},
	})
}

func WaitDeployReadyAndReport(kubeClient *kubernetes.Clientset, functionNamespace string, functionName string) (types.FunctionStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	watch, err := kubeClient.AppsV1().Deployments(functionNamespace).Watch(ctx, metav1.ListOptions{LabelSelector: fmt.Sprintf("faas_function=%s", functionName)})
	if err != nil {
		fmt.Printf("Unable to watch function: %v", err.Error())
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
				fmt.Println("Deployment is ready")
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
