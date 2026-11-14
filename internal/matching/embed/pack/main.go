// Command pack turns an upstream static embedding model into the form the image ships: the first dims columns as
// float16 next to the unchanged tokenizer.json (Dockerfile stage "embed", ADR 0029).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/1xxn/getafjob/internal/matching/embed"
)

func main() {
	from := flag.String("from", "", "directory with tokenizer.json and model.safetensors")
	to := flag.String("to", "", "output directory")
	dims := flag.Int("dims", 256, "columns to keep")
	flag.Parse()
	if *from == "" || *to == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := embed.Pack(*from, *to, *dims); err != nil {
		fmt.Fprintln(os.Stderr, "pack:", err)
		os.Exit(1)
	}
}
