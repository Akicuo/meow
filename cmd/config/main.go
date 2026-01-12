package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/patrickbucher/meow"
	"github.com/valkey-io/valkey-go"
)

func main() {
	addr := flag.String("addr", "0.0.0.0", "listen to address")
	port := flag.Uint("port", 8000, "listen on port")
	flag.Parse()

	log.SetOutput(os.Stderr)

	// Read Valkey URL from environment variable
	valkeyURL := os.Getenv("VALKEY_URL")
	if valkeyURL == "" {
		log.Fatal("VALKEY_URL environment variable is not set")
	}

	// Create Valkey connection
	options := valkey.ClientOption{
		InitAddress: []string{valkeyURL},
		SelectDB:    28,
	}
	client, err := valkey.NewClient(options)
	if err != nil {
		log.Fatalf("create valkey client: %v", err)
	}
	defer client.Close()

	http.HandleFunc("/endpoints/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getEndpoint(w, r, client)
		case http.MethodPost:
			postEndpoint(w, r, client)
		case http.MethodDelete:
			deleteEndpoint(w, r, client)
		default:
			log.Printf("request from %s rejected: method %s not allowed",
				r.RemoteAddr, r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/endpoints", func(w http.ResponseWriter, r *http.Request) {
		getEndpoints(w, r, client)
	})

	listenTo := fmt.Sprintf("%s:%d", *addr, *port)
	log.Printf("listen to %s", listenTo)
	http.ListenAndServe(listenTo, nil)
}

func getEndpoint(w http.ResponseWriter, r *http.Request, vk valkey.Client) {
	log.Printf("GET %s from %s", r.URL, r.RemoteAddr)
	identifier, err := extractEndpointIdentifier(r.URL.String())
	if err != nil {
		log.Printf("extract endpoint identifier of %s: %v", r.URL, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Build the key for this endpoint
	key := fmt.Sprintf("endpoints:%s", identifier)

	// Get the hash values from Valkey
	kvs, err := vk.Do(ctx, vk.B().Hgetall().Key(key).Build()).AsStrMap()
	if err != nil {
		log.Printf("hgetall %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Check if the endpoint exists (empty map means key doesn't exist)
	if len(kvs) == 0 {
		log.Printf(`no such endpoint "%s"`, identifier)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Convert status_online to uint16
	statusOnline, err := strconv.ParseUint(kvs["status_online"], 10, 16)
	if err != nil {
		log.Printf("parse status_online for %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Convert fail_after to uint8
	failAfter, err := strconv.ParseUint(kvs["fail_after"], 10, 8)
	if err != nil {
		log.Printf("parse fail_after for %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	payload := meow.EndpointPayload{
		Identifier:   kvs["identifier"],
		URL:          kvs["url"],
		Method:       kvs["method"],
		StatusOnline: uint16(statusOnline),
		Frequency:    kvs["frequency"],
		FailAfter:    uint8(failAfter),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("serialize payload: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

func postEndpoint(w http.ResponseWriter, r *http.Request, vk valkey.Client) {
	log.Printf("POST %s from %s", r.URL, r.RemoteAddr)
	buf := bytes.NewBufferString("")
	io.Copy(buf, r.Body)
	defer r.Body.Close()
	endpoint, err := meow.EndpointFromJSON(buf.String())
	if err != nil {
		log.Printf("parse JSON body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Build the key for this endpoint
	key := fmt.Sprintf("endpoints:%s", endpoint.Identifier)

	// Check if endpoint exists
	exists, err := vk.Do(ctx, vk.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		log.Printf("check exists %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var status int
	if exists > 0 {
		// updating existing endpoint
		identifierPathParam, err := extractEndpointIdentifier(r.URL.String())
		if err != nil {
			log.Printf("extract endpoint identifier of %s: %v", r.URL, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if identifierPathParam != endpoint.Identifier {
			log.Printf("identifier mismatch: (ressource: %s, body: %s)",
				identifierPathParam, endpoint.Identifier)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		status = http.StatusNoContent
	} else {
		status = http.StatusCreated
	}

	// Store the endpoint in Valkey using HSET
	err = vk.Do(ctx, vk.B().Hset().Key(key).
		FieldValue().
		FieldValue("identifier", endpoint.Identifier).
		FieldValue("url", endpoint.URL.String()).
		FieldValue("method", endpoint.Method).
		FieldValue("status_online", strconv.FormatUint(uint64(endpoint.StatusOnline), 10)).
		FieldValue("frequency", endpoint.Frequency.String()).
		FieldValue("fail_after", strconv.FormatUint(uint64(endpoint.FailAfter), 10)).
		Build()).Error()

	if err != nil {
		log.Printf("hset %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
}

func deleteEndpoint(w http.ResponseWriter, r *http.Request, vk valkey.Client) {
	log.Printf("DELETE %s from %s", r.URL, r.RemoteAddr)
	identifier, err := extractEndpointIdentifier(r.URL.String())
	if err != nil {
		log.Printf("extract endpoint identifier of %s: %v", r.URL, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// Build the key for this endpoint
	key := fmt.Sprintf("endpoints:%s", identifier)

	// Check if endpoint exists
	exists, err := vk.Do(ctx, vk.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		log.Printf("check exists %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if exists == 0 {
		log.Printf(`no such endpoint "%s"`, identifier)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Delete the endpoint from Valkey
	err = vk.Do(ctx, vk.B().Del().Key(key).Build()).Error()
	if err != nil {
		log.Printf("delete %s: %v", key, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func getEndpoints(w http.ResponseWriter, r *http.Request, vk valkey.Client) {
	if r.Method != http.MethodGet {
		log.Printf("request from %s rejected: method %s not allowed",
			r.RemoteAddr, r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	log.Printf("GET %s from %s", r.URL, r.RemoteAddr)

	ctx := context.Background()

	// Get all endpoint keys from Valkey
	keys, err := vk.Do(ctx, vk.B().Keys().Pattern("endpoints:*").Build()).AsStrSlice()
	if err != nil {
		log.Printf("get keys for endpoints:*: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	payloads := make([]meow.EndpointPayload, 0)

	// For each key, get the hash values
	for _, key := range keys {
		kvs, err := vk.Do(ctx, vk.B().Hgetall().Key(key).Build()).AsStrMap()
		if err != nil {
			log.Printf("hgetall %s: %v", key, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Convert status_online to uint16
		statusOnline, err := strconv.ParseUint(kvs["status_online"], 10, 16)
		if err != nil {
			log.Printf("parse status_online for %s: %v", key, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Convert fail_after to uint8
		failAfter, err := strconv.ParseUint(kvs["fail_after"], 10, 8)
		if err != nil {
			log.Printf("parse fail_after for %s: %v", key, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		payload := meow.EndpointPayload{
			Identifier:   kvs["identifier"],
			URL:          kvs["url"],
			Method:       kvs["method"],
			StatusOnline: uint16(statusOnline),
			Frequency:    kvs["frequency"],
			FailAfter:    uint8(failAfter),
		}
		payloads = append(payloads, payload)
	}

	data, err := json.Marshal(payloads)
	if err != nil {
		log.Printf("serialize payloads: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

const endpointIdentifierPatternRaw = "^/endpoints/([a-z][-a-z0-9]+)$"

var endpointIdentifierPattern = regexp.MustCompile(endpointIdentifierPatternRaw)

func extractEndpointIdentifier(endpoint string) (string, error) {
	matches := endpointIdentifierPattern.FindStringSubmatch(endpoint)
	if len(matches) == 0 {
		return "", fmt.Errorf(`endpoint "%s" does not match pattern "%s"`,
			endpoint, endpointIdentifierPatternRaw)
	}
	return matches[1], nil
}
