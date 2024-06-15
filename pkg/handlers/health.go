// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/openfaas/faas-netes/pkg/catalog"
)

const (
	// CPU average overload threshold within one minitues
	CPUOverloadThreshold = 0.80
	// Memory average overload threshold within one minitues
	MemOverloadThreshold = 0.80
)

type CustomHealth struct {
	Overload bool `json:"overload"`
}

// MakeHealthHandler returns 200/OK when healthy
func MakeHealthHandler(node *catalog.Node) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		overload_showed := r.URL.Query().Get("overload")
		// If there are no values associated with the key, Get returns the empty string
		if overload_showed == "0" || overload_showed == "" {
			w.WriteHeader(http.StatusOK)
			return
		}

		overload := node.Overload

		jsonOut, err := json.Marshal(CustomHealth{overload})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}

// func GetExertnalPressure(resolver proxy.BaseURLResolver) (bool, error) {

// 	hostUrl, err := resolver.Resolve("")
// 	if err != nil {
// 		err := fmt.Errorf("unable to resolve remote host: %v", err)
// 		return false, err
// 	}
// 	healthzUrl := fmt.Sprintf("%s/healthz?overload=1", hostUrl.String())
// 	resp, err := http.Get(healthzUrl)
// 	if err != nil {
// 		err := fmt.Errorf("unable to get health: %v", err)
// 		return false, err
// 	}
// 	defer resp.Body.Close()

// 	// defer upstreamCall.Body.Close()

// 	var health = CustomHealth{Overload: false}

// 	body, _ := io.ReadAll(resp.Body)
// 	err = json.Unmarshal(body, &health)
// 	if err != nil {
// 		log.Printf("Error unmarshalling provider json from body %s. Error %s\n", body, err.Error())
// 	}

// 	return health.Overload, nil

// }
