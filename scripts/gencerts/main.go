// gencerts generates a local PKI into ./certs. Run via `make certs`.
package main

import (
	"log"
	"github.com/anjalitiwari/job-worker/internal/certgen"
)

func main() {
	if err := certgen.Generate("certs", "alice", "bob"); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote ca, server, alice, bob to certs/")
}