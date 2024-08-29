// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/openfaas/faas-netes/pkg/catalog"
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
