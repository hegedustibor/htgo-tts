// Command proxy fetches speech from the Google Translate endpoint through an
// HTTP, HTTPS or SOCKS5 proxy and plays it with mplayer:
//
//	go run ./examples/proxy -proxy http://user:password@proxy.example.com:3128 "Hello, world!"
//	go run ./examples/proxy -proxy socks5://localhost:1080 "Hello, world!"
//
// google.WithProxy keeps TLS verification on. A proxy that inspects TLS with
// a certificate of its own belongs in the system's trust store; the -insecure
// flag shows the other way, an http.Client of your own given with
// google.WithHTTPClient, which here skips the verification. Use that only with
// a proxy you trust, because it lets anyone between you and the proxy read
// and change the traffic.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	htgotts "github.com/hegedustibor/htgo-tts/v2"
	"github.com/hegedustibor/htgo-tts/v2/engine/google"
	"github.com/hegedustibor/htgo-tts/v2/voices"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	proxy := flag.String("proxy", "", "proxy URL, such as http://proxy.example.com:3128 or socks5://localhost:1080 (required)")
	insecure := flag.Bool("insecure", false, "skip TLS verification through an http.Client of your own")
	lang := flag.String("lang", string(voices.English), "language of the Google Translate engine, such as en, en-GB or hu")
	timeout := flag.Duration("timeout", 30*time.Second, "time limit for the download and the playback together")
	flag.Usage = usage
	flag.Parse()

	if *proxy == "" {
		return errors.New("the -proxy flag is required")
	}
	text := strings.Join(flag.Args(), " ")
	if text == "" {
		text = "Hello, world!"
	}

	var opts []google.Option
	if *insecure {
		// WithProxy and WithHTTPClient exclude each other, so the client's
		// transport carries the proxy as well as the TLS settings.
		proxyURL, err := url.Parse(*proxy)
		if err != nil {
			return err
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(proxyURL)
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		opts = append(opts, google.WithHTTPClient(&http.Client{Transport: transport}))
	} else {
		opts = append(opts, google.WithProxy(*proxy))
	}

	tts, err := htgotts.New(google.New(voices.Language(*lang), opts...))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return tts.Speak(ctx, text)
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "usage: proxy -proxy url [flags] text...")
	flag.PrintDefaults()
}
