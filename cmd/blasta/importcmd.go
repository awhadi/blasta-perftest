package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/awhadi/blasta-perftest/internal/config"
	"github.com/awhadi/blasta-perftest/internal/importer"
)

// jobFrom turns an imported request into a job file with the default load settings.
func jobFrom(r importer.Request) config.Job {
	j := config.DefaultJob()
	j.Name = r.Name
	j.Method = r.Method
	j.Target.URL = r.URL
	j.Headers = r.Headers
	j.Body = r.Body
	return j
}

// cmdImport reads a curl command, a HAR file, a Postman collection or an OpenAPI document and
// writes a job file for one of the requests in it.
func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	list := fs.Bool("list", false, "list the requests found instead of writing a job")
	index := fs.Int("index", 1, "which request to write, as numbered by --list")
	out := fs.String("out", "", "write the job here (default: print it)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: blasta import [--list] [--index N] [--out job.json] <file|->\n\nReads a curl command, a HAR file, a Postman collection or an OpenAPI/Swagger document\n(a file, or - for standard input) and writes a job file for one of its requests.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("name the file to import, or - for standard input")
	}
	var data []byte
	var err error
	if fs.Arg(0) == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, importer.MaxInput+1))
	} else {
		data, err = os.ReadFile(fs.Arg(0))
	}
	if err != nil {
		return err
	}
	res, err := importer.Parse(string(data))
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "note:", w)
	}
	if *list {
		fmt.Printf("%s: %d requests", res.Format, len(res.Requests))
		if res.Skipped > 0 {
			fmt.Printf(" (%d images, scripts and similar left out)", res.Skipped)
		}
		fmt.Println()
		for i, r := range res.Requests {
			fmt.Printf("%4d  %-7s %s\n", i+1, r.Method, r.URL)
			for _, n := range r.Notes {
				fmt.Printf("      note: %s\n", n)
			}
		}
		return nil
	}
	if *index < 1 || *index > len(res.Requests) {
		return fmt.Errorf("--index must be between 1 and %d", len(res.Requests))
	}
	r := res.Requests[*index-1]
	for _, n := range r.Notes {
		fmt.Fprintln(os.Stderr, "note:", n)
	}
	b, err := json.MarshalIndent(jobFrom(r), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if *out == "" {
		_, err = os.Stdout.Write(b)
		return err
	}
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%s %s): check it with  blasta check %s\n", *out, r.Method, strings.TrimSpace(r.URL), *out)
	return nil
}

// reorder puts the flags first, so "blasta import file.json --list" works as well as
// "blasta import --list file.json".
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && (strings.TrimLeft(a, "-") == "index" || strings.TrimLeft(a, "-") == "out") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}
