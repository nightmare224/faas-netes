package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	weightedrand "github.com/mroth/weightedrand/v2"
	"github.com/openfaas/faas-netes/pkg/catalog"
	fhttputil "github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/proxy"
	types "github.com/openfaas/faas-provider/types"
)

// The factory that affect the weighted round robin (exponential)
const weightRRfactory = 2

// Make this able to search for other cluster's function.
// And if other cluster exist the function, them deploy it on current local one
// and trigger on local cluster
func MakeTriggerHandler(functionNamespace string, config types.FaaSConfig, c catalog.Catalog) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		// means in multi cluster scenario
		// if !isOffloadRequest(r) {
		vars := mux.Vars(r)
		functionName := vars["name"]
		if strings.Contains(vars["name"], ".") {
			functionName = strings.TrimSuffix(vars["name"], "."+functionNamespace)
		}
		_, exist := c.FunctionCatalog[functionName]
		if !exist {
			err := fmt.Errorf("no endpoints available for: %s", functionName)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		targetP2PID := catalog.GetSelfCatalogKey()
		var err error
		if !isOffloadRequest(r) && catalog.EnabledOffload {
			targetP2PID, err = findSuitableNode(functionName, c)
			if err != nil {
				fmt.Printf("Unable to trigger function: %v\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		// if the target node has no function, trigger the deploy first
		if replicas, exist := c.NodeCatalog[targetP2PID].AvailableFunctionsReplicas[functionName]; !exist || replicas == 0 {
			deployFunctionByP2PID(functionNamespace, functionName, targetP2PID, c)
			// wait until the function is ready
			_, err := catalog.WaitDeployReadyAndReport(c.NodeCatalog[targetP2PID].Clientset, functionNamespace, functionName)
			if err != nil {
				log.Printf("[Deploy] error deploying %s, error: %s\n", functionName, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		// trigger the function here
		// if non local than change the resolver
		markAsOffloadRequest(r)
		offloadRequest(w, r, config, c.NodeCatalog[targetP2PID].InvokeResolver, c.NodeCatalog[targetP2PID].FunctionExecutionTime)

		// create a replica at the trigger point
		if replicas, exist := c.NodeCatalog[catalog.GetSelfCatalogKey()].AvailableFunctionsReplicas[functionName]; !exist || replicas == 0 {
			go deployFunctionByP2PID(functionNamespace, functionName, catalog.GetSelfCatalogKey(), c)
		}
	}
}
func markAsOffloadRequest(r *http.Request) {
	r.URL.RawQuery = "offload=1"
}
func isOffloadRequest(r *http.Request) bool {
	offload := r.URL.Query().Get("offload")

	// If there are no values associated with the key, Get returns the empty string
	return offload == "1"
}

// return the select p2pid to execution function based on last weighted exec time
func weightExecTimeScheduler(functionName string, NodeCatalog map[string]*catalog.Node) (string, error) {

	// var choices []*weightedrand.Chooser[T, W]
	var execTimeProd int64 = 1
	p2pIDExecTimeMapping := make(map[string]int64)
	for p2pID, node := range NodeCatalog {
		// skip the overload node
		if node.Overload {
			continue
		}
		if replicas, exist := node.AvailableFunctionsReplicas[functionName]; exist && replicas > 0 {
			// no execTime record yet, just gave one (in fact it already init as 1)
			// execTime := time.Duration(1)
			// if t, exist := node.FunctionExecutionTime[functionName]; exist {
			// execTime = t
			execTimeRaw := node.FunctionExecutionTime[functionName].Load()
			// do power in int
			execTime := execTimeRaw
			for i := 1; i < weightRRfactory; i++ {
				execTime *= execTimeRaw
			}
			execTimeProd *= execTime
			p2pIDExecTimeMapping[p2pID] = execTime
		}
	}

	// all the node with funtion is overload
	if len(p2pIDExecTimeMapping) == 0 {
		return "", fmt.Errorf("no non-overloaded node to execution function: %s", functionName)
	}

	choices := make([]weightedrand.Choice[string, int64], 0)
	// rightProd := make([]time.Duration, len(leftProd))
	// rightProd[len(leftProd)-1] = 1
	// for i := len(execTimeList) - 1; i >= 0; i-- {
	// 	choices = append(choices, weightedrand.NewChoice(p2pIDList[i], rightProd[i]*leftProd[i]))
	// 	rightProd[i-1] = rightProd[i] * execTimeList[i]
	// }

	for p2pID, execTime := range p2pIDExecTimeMapping {
		probability := execTimeProd / execTime
		choices = append(choices, weightedrand.NewChoice(p2pID, probability))
		log.Printf("exec time map %s: %d (probability: %d)\n", p2pID, execTime, probability)
	}
	chooser, _ := weightedrand.NewChooser(
		choices...,
	)

	return chooser.Pick(), nil
}

// find the information of functionstatus, and found the one can be deployed/triggered this function
// if the first parameter is nil, mean do not require deploy before trigger
func findSuitableNode(functionName string, c catalog.Catalog) (string, error) {

	p2pID, err := weightExecTimeScheduler(functionName, c.NodeCatalog)
	// if can not found the suitable node to execute function, report the first non-overload node
	if err != nil {
		for p2pID, node := range c.NodeCatalog {
			if !node.Overload {
				return p2pID, nil
			}
		}
	}
	return p2pID, nil

}

// maybe in other place when the platform is overload the request can be redirect
func offloadRequest(w http.ResponseWriter, r *http.Request, config types.FaaSConfig, resolver proxy.BaseURLResolver, functionExecutionTime map[string]*atomic.Int64) {
	if resolver == nil {
		panic("NewHandlerFunc: empty proxy handler resolver, cannot be nil")
	}
	// a http client for sending request
	proxyClient := proxy.NewProxyClientFromConfig(config)

	reverseProxy := httputil.ReverseProxy{}
	reverseProxy.Director = func(req *http.Request) {
		// At least an empty director is required to prevent runtime errors.
		req.URL.Scheme = "http"
	}
	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
	}

	// Errors are common during disconnect of client, no need to log them.
	reverseProxy.ErrorLog = log.New(io.Discard, "", 0)

	if r.Body != nil {
		defer r.Body.Close()
	}

	// only allow the Get and Post request
	if r.Method == http.MethodPost || r.Method == http.MethodGet {
		proxyRequest(w, r, proxyClient, resolver, &reverseProxy, functionExecutionTime)
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// basically the below is just copy paste from "github.com/openfaas/faas-provider/proxy"
const (
	defaultContentType     = "text/plain"
	openFaaSInternalHeader = "X-OpenFaaS-Internal"
)

// proxyRequest handles the actual resolution of and then request to the function service.
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver proxy.BaseURLResolver, reverseProxy *httputil.ReverseProxy, functionExecutionTime map[string]*atomic.Int64) {
	ctx := originalReq.Context()

	pathVars := mux.Vars(originalReq)
	functionName := pathVars["name"]
	if functionName == "" {
		w.Header().Add(openFaaSInternalHeader, "proxy")

		fhttputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
		return
	}

	functionAddr, err := resolver.Resolve(functionName)
	// fmt.Printf("Function Address: %+v\n\n", functionAddr)
	// fmt.Printf("Original Request: %+v\n\n", originalReq)
	if err != nil {
		w.Header().Add(openFaaSInternalHeader, "proxy")

		// TODO: Should record the 404/not found error in Prometheus.
		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, err.Error())
		fhttputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
		return
	}

	proxyReq, err := buildProxyRequest(originalReq, functionAddr, "/function/"+functionName)
	// fmt.Printf("\nProxy Req: %+v\n", proxyReq)
	if err != nil {

		w.Header().Add(openFaaSInternalHeader, "proxy")

		fhttputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
		return
	}

	if proxyReq.Body != nil {
		defer proxyReq.Body.Close()
	}

	start := time.Now()
	defer func() {
		nanoseconds := time.Since(start)
		// log.Printf("%s took %f seconds\n", functionName, seconds.Seconds())
		actualFunctionName := functionName
		if strings.Contains(functionName, ".") {
			actualFunctionName = strings.TrimSuffix(functionName, "."+"openfaas-fn")
		}
		// does the update too often?
		// just store milliseconds
		functionExecutionTime[actualFunctionName].Store(int64(nanoseconds / 1000000))
	}()

	if v := originalReq.Header.Get("Accept"); v == "text/event-stream" {
		originalReq.URL = proxyReq.URL

		reverseProxy.ServeHTTP(w, originalReq)
		return
	}

	response, err := proxyClient.Do(proxyReq.WithContext(ctx))
	if err != nil {
		log.Printf("\nerror with proxy request to: %s, %s\n", proxyReq.URL.String(), err.Error())

		w.Header().Add(openFaaSInternalHeader, "proxy")

		fhttputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for: %s.", functionName)
		return
	}

	if response.Body != nil {
		defer response.Body.Close()
	}

	clientHeader := w.Header()
	copyHeaders(clientHeader, &response.Header)
	w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))

	w.WriteHeader(response.StatusCode)
	if response.Body != nil {
		io.Copy(w, response.Body)
	}
}

// buildProxyRequest creates a request object for the proxy request, it will ensure that
// the original request headers are preserved as well as setting openfaas system headers
func buildProxyRequest(originalReq *http.Request, baseURL url.URL, extraPath string) (*http.Request, error) {

	host := baseURL.Host
	// if baseURL.Port() == "" {
	// 	host = baseURL.Host + ":" + watchdogPort
	// }

	url := url.URL{
		Scheme:   baseURL.Scheme,
		Host:     host,
		Path:     extraPath,
		RawQuery: originalReq.URL.RawQuery,
	}

	upstreamReq, err := http.NewRequest(originalReq.Method, url.String(), nil)
	if err != nil {
		return nil, err
	}
	copyHeaders(upstreamReq.Header, &originalReq.Header)

	if len(originalReq.Host) > 0 && upstreamReq.Header.Get("X-Forwarded-Host") == "" {
		upstreamReq.Header["X-Forwarded-Host"] = []string{originalReq.Host}
	}
	if upstreamReq.Header.Get("X-Forwarded-For") == "" {
		upstreamReq.Header["X-Forwarded-For"] = []string{originalReq.RemoteAddr}
	}

	if originalReq.Body != nil {
		upstreamReq.Body = originalReq.Body
	}

	return upstreamReq, nil
}

// copyHeaders clones the header values from the source into the destination.
func copyHeaders(destination http.Header, source *http.Header) {
	for k, v := range *source {
		vClone := make([]string, len(v))
		copy(vClone, v)
		destination[k] = vClone
	}
}

// getContentType resolves the correct Content-Type for a proxied function.
func getContentType(request http.Header, proxyResponse http.Header) (headerContentType string) {
	responseHeader := proxyResponse.Get("Content-Type")
	requestHeader := request.Get("Content-Type")

	if len(responseHeader) > 0 {
		headerContentType = responseHeader
	} else if len(requestHeader) > 0 {
		headerContentType = requestHeader
	} else {
		headerContentType = defaultContentType
	}

	return headerContentType
}
