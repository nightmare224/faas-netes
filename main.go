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
	"sync/atomic"

	"github.com/openfaas/faas-netes/pkg/catalog"
	"github.com/openfaas/faas-netes/pkg/signals"

	"github.com/openfaas/faas-netes/pkg/config"
	"github.com/openfaas/faas-netes/pkg/handlers"
	"github.com/openfaas/faas-netes/pkg/k8s"
	version "github.com/openfaas/faas-netes/version"
	faasProvider "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/logs"
	providertypes "github.com/openfaas/faas-provider/types"
	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	// required to authenticate against GKE clusters
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	// required for updating and validating the CRD clientset
	_ "k8s.io/code-generator/cmd/client-gen/generators"
	// main.go:36:2: import "sigs.k8s.io/controller-tools/cmd/controller-gen" is a program, not an importable package
	// _ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)

// const defaultResync = time.Hour * 10
// const selfSetupIP = "0.0.0.0"

func main() {
	var kubeconfig string
	var masterURL string
	var (
		verbose bool
	)
	var kubeconfigPath string
	var offload bool

	flag.StringVar(&kubeconfig, "kubeconfig", "",
		"Path to a kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&verbose, "verbose", false, "Print verbose config information")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.Bool("operator", false, "Run as an operator (not available in CE)")
	flag.StringVar(&kubeconfigPath, "kubeconfigpath", "",
		"Path to a kubeconfig directory. Only required if multiple OpenFaaS clusters exist.")
	flag.BoolVar(&offload, "offload", false,
		"Enable offload to other cluster. Only work if multiple OpenFaaS clusters exist.")

	flag.Parse()

	mode := "controller"
	catalog.EnabledOffload = offload

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
	log.Println("ClientCmdConfigs len:", len(clientCmdConfigs))

	// map ip to config
	clientCmdConfigMap := make(map[string]*rest.Config)
	for i, clientCmdConfig := range clientCmdConfigs {
		if i == 0 {
			clientCmdConfigMap[catalog.GetSelfFaasP2PIp()] = clientCmdConfig
		} else {
			parsedURL, _ := url.Parse(clientCmdConfig.Host)
			ip, _, _ := net.SplitHostPort(parsedURL.Host)
			clientCmdConfigMap[ip] = clientCmdConfig
		}
	}
	// log.Println("ClientCmdConfigMap:", clientCmdConfigMap)

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

	runController(config, clientCmdConfigMap)
}

// runController runs the faas-netes imperative controller
func runController(config config.BootstrapConfig, clientCmdConfigMap map[string]*rest.Config) {

	// ipKubeMapping := make(map[string]catalog.KubeClient)
	// var functionList *k8s.FunctionList = nil
	operator := false
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	// create function
	NewKubeClientWithIp := catalog.NewKubeClientWithIpGenerator(config, clientCmdConfigMap, stopCh, operator)

	// create the catalog to store p
	c := catalog.NewCatalog(NewKubeClientWithIp)
	node := initSelfCatagory(c, config.DefaultFunctionNamespace)
	// create catalog
	InitNetworkErr := catalog.InitInfoNetwork(c)
	if InitNetworkErr != nil {
		err := fmt.Errorf("cannot init info network: %s", InitNetworkErr)
		panic(err)
	}

	// start the local update
	promClient := initPromClient()
	go node.ListenUpdateInfo(&promClient)

	// kubeP2PMappingList := catalog.NewKubeP2PMappingList(ipKubeMapping, selfSetupIP, c)
	localKubeClient := c.NodeCatalog[catalog.GetSelfCatalogKey()].KubeClient
	localFunctionList := k8s.NewFunctionList(config.DefaultFunctionNamespace, localKubeClient.DeployLister)

	// create a handle a pass the config into, so the closure can hold the config
	// printFunctionExecutionTime := true
	bootstrapHandlers := providertypes.FaaSHandlers{
		// FunctionProxy:  proxy.NewHandlerFunc(config.FaaSConfig, functionLookupInterfaces, printFunctionExecutionTime),
		FunctionProxy:  handlers.MakeTriggerHandler(config.DefaultFunctionNamespace, config.FaaSConfig, c),
		DeleteFunction: handlers.MakeDeleteHandler(config.DefaultFunctionNamespace, localKubeClient.Clientset, c),
		// deploy on local cluster (index[0])
		DeployFunction: handlers.MakeDeployHandler(config.DefaultFunctionNamespace, localKubeClient.Factory, localFunctionList, localKubeClient.Clientset, c),
		FunctionLister: handlers.MakeFunctionReader(config.DefaultFunctionNamespace, c),
		FunctionStatus: handlers.MakeReplicaReader(config.DefaultFunctionNamespace, c),
		ScaleFunction:  handlers.MakeReplicaUpdater(config.DefaultFunctionNamespace, c),
		UpdateFunction: handlers.MakeUpdateHandler(config.DefaultFunctionNamespace, localKubeClient.Factory),
		Health:         handlers.MakeHealthHandler(node),
		Info:           handlers.MakeInfoHandler(version.BuildVersion(), version.GitCommit),
		Secrets:        handlers.MakeSecretHandler(config.DefaultFunctionNamespace, localKubeClient.Clientset),
		Logs:           logs.NewLogHandlerFunc(k8s.NewLogRequestor(localKubeClient.Clientset, config.DefaultFunctionNamespace), config.FaaSConfig.WriteTimeout),
		ListNamespaces: handlers.MakeNamespacesLister(config.DefaultFunctionNamespace, localKubeClient.Clientset),
	}

	ctx := context.Background()

	faasProvider.Serve(ctx, &bootstrapHandlers, &config.FaaSConfig)
}

func initSelfCatagory(c catalog.Catalog, functionNamespace string) *catalog.Node {
	c.NewNodeCatalogEntry(catalog.GetSelfCatalogKey(), catalog.GetSelfFaasP2PIp())

	// init available function to catalog
	deploymentLister := c.NodeCatalog[catalog.GetSelfCatalogKey()].DeployLister
	fns, err := handlers.ListFunctionStatus(functionNamespace, deploymentLister)
	if err != nil {
		fmt.Printf("cannot init available function: %s", err)
		panic(err)
	}

	// register the event handler so when there is the change it publish to p2p network
	node := c.NodeCatalog[catalog.GetSelfCatalogKey()]
	deployInformer := node.Informers.DeploymentInformer
	clientset := node.Clientset
	// catalog.RegisterEventHandlers(c, deployInformer, clientset, functionNamespace)
	c.RegisterEventHandlers(deployInformer, clientset, functionNamespace)

	for i, fn := range fns {
		// TODO: should be more sphofisticate
		c.FunctionCatalog[fn.Name] = &fns[i]
		c.NodeCatalog[catalog.GetSelfCatalogKey()].AvailableFunctionsReplicas[fn.Name] = fn.AvailableReplicas
		c.NodeCatalog[catalog.GetSelfCatalogKey()].FunctionExecutionTime[fn.Name] = new(atomic.Int64)
		c.NodeCatalog[catalog.GetSelfCatalogKey()].FunctionExecutionTime[fn.Name].Store(1)
	}

	return c.NodeCatalog[catalog.GetSelfCatalogKey()]
}

func initPromClient() promv1.API {
	address := "http://prometheus.openfaas.svc:9090"
	promClient, _ := promapi.NewClient(promapi.Config{
		Address: address,
	})

	promAPIClient := promv1.NewAPI(promClient)

	return promAPIClient
}
