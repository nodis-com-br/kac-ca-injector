/*
 * Kubernetes Admission Controller
 *
 * This is a generic definition for a Kubernetes Admission Controller
 *
 * API version: 1.0.0
 * Contact: infra@nodis.com.br
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package main

import (
	"flag"
	"log"

	kac "github.com/nodis-com-br/kac-ca-injector/pkg"
)

func main() {
	var tlsKey, tlsCert string
	flag.StringVar(&tlsKey, "tlsKey", "/certs/tls.key", "Path to the TLS key")
	flag.StringVar(&tlsCert, "tlsCert", "/certs/tls.crt", "Path to the TLS certificate")
	flag.Parse()
	log.Printf("Server started")
	router := kac.NewRouter()
	log.Fatal(router.RunTLS(":8443", tlsCert, tlsKey))
}
