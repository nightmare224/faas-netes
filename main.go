// Copyright (c) Alex Ellis 2017. All rights reserved.
// Copyright (c) OpenFaaS Author(s) 2020. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	clientset "github.com/openfaas/faas-netes/pkg/client/clientset/versioned"
	informers "github.com/openfaas/faas-netes/pkg/client/informers/externalversions"
	v1 "github.com/openfaas/faas-netes/pkg/client/informers/externalversions/openfaas/v1"
	"github.com/openfaas/faas-netes/pkg/config"
	"github.com/openfaas/faas-netes/pkg/handlers"
	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-netes/pkg/signals"
	version "github.com/openfaas/faas-netes/version"
	faasProvider "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/logs"
	"github.com/openfaas/faas-provider/proxy"
	providertypes "github.com/openfaas/faas-provider/types"

	kubeinformers "k8s.io/client-go/informers"
	v1apps "k8s.io/client-go/informers/apps/v1"
	v1core "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	v1appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	// required to authenticate against GKE clusters
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	// required for updating and validating the CRD clientset
	_ "k8s.io/code-generator/cmd/client-gen/generators"
	// main.go:36:2: import "sigs.k8s.io/controller-tools/cmd/controller-gen" is a program, not an importable package
	// _ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)

const defaultResync = time.Hour * 10

func main() {
	var kubeconfig string
	var masterURL string
	var (
		verbose bool
	)
	var kubeconfigPath string

	flag.StringVar(&kubeconfig, "kubeconfig", "",
		"Path to a kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&verbose, "verbose", false, "Print verbose config information")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.Bool("operator", false, "Run as an operator (not available in CE)")
	flag.StringVar(&kubeconfigPath, "kubeconfigpath", "",
		"Path to a kubeconfig directory. Only required if multiple OpenFaaS clusters exist.")

	flag.Parse()

	mode := "controller"

	sha, release := version.GetReleaseInfo()
	fmt.Printf("faas-netes - Community Edition (CE)\n"+
		"Warning: Commercial use limited to 60 days.\n"+
		"\nVersion: %s Commit: %s Mode: %s\n", release, sha, mode)

	if err := config.ConnectivityCheck(); err != nil {
		log.Fatalf("Error checking connectivity, OpenFaaS CE cannot be run in an offline environment: %s", err.Error())
	}

	var clientCmdConfigs []*rest.Config
	if kubeconfigPath != "" {
		entries, err := os.ReadDir(kubeconfigPath)
		if err != nil {
			log.Fatalf("Error building kubeconfig path: %s", err.Error())
		}

		for i, e := range entries {
			kubeconfig := kubeconfigPath + e.Name()
			config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				log.Fatalf("Error building kubeconfig: %s", err.Error())
			}
			clientCmdConfigs = append(clientCmdConfigs, config)
			fmt.Printf("Host: %s, APIPath: %s\n", clientCmdConfigs[i].Host, clientCmdConfigs[i].APIPath)
		}
	} else {
		config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
		if err != nil {
			log.Fatalf("Error building kubeconfig: %s", err.Error())
		}
		clientCmdConfigs = append(clientCmdConfigs, config)
	}

	// debug
	// fmt.Printf("debug msg, masterURL: %s", masterURL)
	// fmt.Printf("debug msg, APIPath: %s, Host: %s\n", clientCmdConfig.APIPath, clientCmdConfig.Host)

	// number of config
	numConfig := len(clientCmdConfigs)
	kubeconfigQPS := 100
	kubeconfigBurst := 250
	kubeClients := make([]*kubernetes.Clientset, numConfig)
	faasClients := make([]*clientset.Clientset, numConfig)
	for i := 0; i < numConfig; i++ {
		// create kubeClients
		clientCmdConfigs[i].QPS = float32(kubeconfigQPS)
		clientCmdConfigs[i].Burst = kubeconfigBurst
		kubeClient, err := kubernetes.NewForConfig(clientCmdConfigs[i])
		if err != nil {
			log.Fatalf("Error building Kubernetes clientset: %s", err.Error())
		}
		kubeClients[i] = kubeClient

		//create faasClients
		faasClient, err := clientset.NewForConfig(clientCmdConfigs[i])
		if err != nil {
			log.Fatalf("Error building OpenFaaS clientset: %s", err.Error())
		}
		faasClients[i] = faasClient
	}

	readConfig := config.ReadConfig{}
	osEnv := providertypes.OsEnv{}
	config, err := readConfig.Read(osEnv)

	if err != nil {
		log.Fatalf("Error reading config: %s", err.Error())
	}

	config.Fprint(verbose)

	deployConfig := k8s.DeploymentConfig{
		RuntimeHTTPPort: 8080,
		HTTPProbe:       config.HTTPProbe,
		SetNonRootUser:  config.SetNonRootUser,
		ReadinessProbe: &k8s.ProbeConfig{
			InitialDelaySeconds: int32(2),
			TimeoutSeconds:      int32(1),
			PeriodSeconds:       int32(2),
		},
		LivenessProbe: &k8s.ProbeConfig{
			InitialDelaySeconds: int32(2),
			TimeoutSeconds:      int32(1),
			PeriodSeconds:       int32(2),
		},
	}

	namespaceScope := config.DefaultFunctionNamespace

	if namespaceScope == "" {
		klog.Fatal("DefaultFunctionNamespace must be set")
	}

	kubeInformerFactories := make([]kubeinformers.SharedInformerFactory, numConfig)
	faasInformerFactories := make([]informers.SharedInformerFactory, numConfig)
	factories := make([]k8s.FunctionFactory, numConfig)
	for i := 0; i < numConfig; i++ {
		kubeInformerOpt := kubeinformers.WithNamespace(namespaceScope)
		kubeInformerFactories[i] = kubeinformers.NewSharedInformerFactoryWithOptions(kubeClients[i], defaultResync, kubeInformerOpt)

		faasInformerOpt := informers.WithNamespace(namespaceScope)
		faasInformerFactories[i] = informers.NewSharedInformerFactoryWithOptions(faasClients[i], defaultResync, faasInformerOpt)

		factories[i] = k8s.NewFunctionFactory(kubeClients[i], deployConfig, faasClients[i].OpenfaasV1())
	}

	setups := make([]serverSetup, numConfig)
	for i := 0; i < numConfig; i++ {
		setups[i] = serverSetup{
			config:              config,
			functionFactory:     factories[i],
			kubeInformerFactory: kubeInformerFactories[i],
			faasInformerFactory: faasInformerFactories[i],
			kubeClient:          kubeClients[i],
			faasClient:          faasClients[i],
		}
	}

	runController(setups)
}

type customInformers struct {
	EndpointsInformer  v1core.EndpointsInformer
	DeploymentInformer v1apps.DeploymentInformer
	FunctionsInformer  v1.FunctionInformer
}

func startInformers(setup serverSetup, stopCh <-chan struct{}, operator bool) customInformers {
	// assume the index 0 is the local cluster
	kubeInformerFactory := setup.kubeInformerFactory
	faasInformerFactory := setup.faasInformerFactory

	var functions v1.FunctionInformer
	if operator {
		functions = faasInformerFactory.Openfaas().V1().Functions()
		go functions.Informer().Run(stopCh)
		if ok := cache.WaitForNamedCacheSync("faas-netes:functions", stopCh, functions.Informer().HasSynced); !ok {
			log.Fatalf("failed to wait for cache to sync")
		}
	}
	// start the informer for Kubernetes deployments in a new goroutine,
	//and listen for events related to deployments until a stop signal is received through the stopCh channel
	deployments := kubeInformerFactory.Apps().V1().Deployments()
	go deployments.Informer().Run(stopCh)
	if ok := cache.WaitForNamedCacheSync("faas-netes:deployments", stopCh, deployments.Informer().HasSynced); !ok {
		log.Fatalf("failed to wait for cache to sync")
	}

	endpoints := kubeInformerFactory.Core().V1().Endpoints()
	go endpoints.Informer().Run(stopCh)
	if ok := cache.WaitForNamedCacheSync("faas-netes:endpoints", stopCh, endpoints.Informer().HasSynced); !ok {
		log.Fatalf("failed to wait for cache to sync")
	}

	return customInformers{
		EndpointsInformer:  endpoints,
		DeploymentInformer: deployments,
		FunctionsInformer:  functions,
	}
}

// runController runs the faas-netes imperative controller
func runController(setup []serverSetup) {
	// assume the index 0 is the local one
	config := setup[0].config
	kubeClient := setup[0].kubeClient
	factory := setup[0].functionFactory
	//fmt.Println(factory.Client.NodeV1().RESTClient().Get())
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()
	operator := false
	listers := startInformers(setup[0], stopCh, operator)
	handlers.RegisterEventHandlers(listers.DeploymentInformer, kubeClient, config.DefaultFunctionNamespace)
	deployLister := listers.DeploymentInformer.Lister()
	functionLookup := k8s.NewFunctionLookup(config.DefaultFunctionNamespace, listers.EndpointsInformer.Lister())
	functionList := k8s.NewFunctionList(config.DefaultFunctionNamespace, deployLister)

	// for external cluster
	deployListers := make([]v1appslisters.DeploymentLister, len(setup))
	deployListers[0] = deployLister
	functionLookupInterfaces := make([]proxy.BaseURLResolver, len(setup))
	functionLookupInterfaces[0] = functionLookup
	//functionLists := make([]*k8s.FunctionList, len(setup))
	//functionLists[0] = functionList
	// external cluster
	for i := 1; i < len(setup); i++ {
		// stopCh := signals.SetupSignalHandler()
		operator := false
		externalListers := startInformers(setup[i], stopCh, operator)
		deployListers[i] = externalListers.DeploymentInformer.Lister()
		// externalFunctionList := k8s.NewFunctionList(config.DefaultFunctionNamespace, deployLister)
		// fmt.Println(externalFunctionList)
		functionLookupInterfaces[i] = k8s.NewFunctionLookup(setup[i].config.DefaultFunctionNamespace, externalListers.EndpointsInformer.Lister())
		//functionLists[i] = k8s.NewFunctionList(config.DefaultFunctionNamespace, deployListers[i])
	}

	// create a handle a pass the config into, so the closure can hold the config
	printFunctionExecutionTime := true
	bootstrapHandlers := providertypes.FaaSHandlers{
		// FunctionProxy:  proxy.NewHandlerFunc(config.FaaSConfig, functionLookupInterfaces, printFunctionExecutionTime),
		FunctionProxy:  handlers.MakeTriggerHandler(config.DefaultFunctionNamespace, config.FaaSConfig, functionLookupInterfaces, printFunctionExecutionTime, deployListers, factory),
		DeleteFunction: handlers.MakeDeleteHandler(config.DefaultFunctionNamespace, kubeClient),
		// deploy on local cluster (index[0])
		DeployFunction: handlers.MakeDeployHandler(config.DefaultFunctionNamespace, factory, functionList),
		FunctionLister: handlers.MakeFunctionReader(config.DefaultFunctionNamespace, deployListers),
		FunctionStatus: handlers.MakeReplicaReader(config.DefaultFunctionNamespace, deployLister),
		ScaleFunction:  handlers.MakeReplicaUpdater(config.DefaultFunctionNamespace, kubeClient),
		UpdateFunction: handlers.MakeUpdateHandler(config.DefaultFunctionNamespace, factory),
		Health:         handlers.MakeHealthHandler(),
		Info:           handlers.MakeInfoHandler(version.BuildVersion(), version.GitCommit),
		Secrets:        handlers.MakeSecretHandler(config.DefaultFunctionNamespace, kubeClient),
		Logs:           logs.NewLogHandlerFunc(k8s.NewLogRequestor(kubeClient, config.DefaultFunctionNamespace), config.FaaSConfig.WriteTimeout),
		ListNamespaces: handlers.MakeNamespacesLister(config.DefaultFunctionNamespace, kubeClient),
	}

	ctx := context.Background()

	faasProvider.Serve(ctx, &bootstrapHandlers, &config.FaaSConfig)
}

// serverSetup is a container for the config and clients needed to start the
// faas-netes controller or operator
type serverSetup struct {
	config              config.BootstrapConfig
	kubeClient          *kubernetes.Clientset
	faasClient          *clientset.Clientset
	functionFactory     k8s.FunctionFactory
	kubeInformerFactory kubeinformers.SharedInformerFactory
	faasInformerFactory informers.SharedInformerFactory
}
