package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type StatusRoot struct {
	Icestats IcecastStats
}

type IcecastStats struct {
	Source Source
}

type Source []Stream

type Stream struct {
	Listeners  int
	ServerName string `json:"server_name"`
	ListenURL  string `json:"listenurl"`
}

func urlToLabel(name string) string {
	i := strings.LastIndex(name, "/")
	if i >= 0 {
		name = name[i:][1:]
	}
	return name
}

func makeLegacyLabel(name string) string {
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, " ", "_")

	re := regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)
	matches := re.FindAllString(name, -1)
	return strings.Join(matches, "")
}

func (sourcePtr *Source) UnmarshalJSON(data []byte) error {
	var multiStream []Stream
	if err := json.Unmarshal(data, &multiStream); err == nil {
		*sourcePtr = multiStream
		return nil
	}

	var singleStream Stream
	if err := json.Unmarshal(data, &singleStream); err == nil {
		*sourcePtr = []Stream{singleStream}
		return nil
	}
	return fmt.Errorf("error parsing icestats source")
}

var listeners = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "icecast_listeners",
	Help: "Gauge representing current Icecast stream listeners",
}, []string{"server_name", "stream_url"})

func LoadIcecastStatus(url string) (stats *StatusRoot, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return
	}

	defer resp.Body.Close()

	stats = new(StatusRoot)

	json.NewDecoder(resp.Body).Decode(&stats)

	return
}

func publishVClock(clock string, listeners int) {
	s := fmt.Sprintf("http://%s/?Command=SetMem=Listeners,%d", clock, listeners)

	resp, err := http.Get(s)
	if err != nil {
		return
	}

	defer resp.Body.Close()

	return
}

func updateListeners(url string, serverName string, wait int, clock string, legacyLabel bool) {
	go func() {
		for {
			resp, err := LoadIcecastStatus(url)

			if err != nil {
				log.Println("Error polling Icecast endpoint, trying again in", wait)
			} else {
				for _, s := range resp.Icestats.Source {
					if s.ServerName == serverName || serverName == "" {
						labelServer := s.ServerName
						labelURL := urlToLabel(s.ListenURL)
						if legacyLabel {
							labelServer = makeLegacyLabel(labelServer)
							labelURL = makeLegacyLabel(labelURL)
						}
						listeners.WithLabelValues(labelServer, labelURL).Set(float64(s.Listeners))
						go publishVClock(clock, s.Listeners)
						// log.Println(labelServer, ",", labelURL, " : ", s.Listeners)
					}
				}
			}

			time.Sleep(15 * time.Second)
		}
	}()
}

func main() {
	urlPtr := flag.String("url", "", "Icecast status endpoint (normally: http://icecast.example.com/status-json.xsl)")
	portPtr := flag.Int("port", 2112, "Port to listen on for metrics")
	endpointPtr := flag.String("endpoint", "/metrics", "Metrics endpoint to listen on")
	waitPtr := flag.Int("interval", 15, "Interval to update statistics from Icecast")
	clockPtr := flag.String("clock", "", "VClock URL")
	serverNamePtr := flag.String("filter", "", "filter for server_name, only streams with this server_name will be collected")
	legacyLabelPtr := flag.Bool("legacy-label", false, "make label names compatible with prometheus < 3.0 (space and dot will be replaced by underscore)")

	flag.Parse()

	if *urlPtr == "" {
		log.Fatalf("Missing required argument -url, see '%s -help' for information", os.Args[0])
	}

	log.Println("Starting Icecast Exporter")

	if *serverNamePtr != "" {
		log.Println("filter for ", *serverNamePtr)
	}

	if *legacyLabelPtr {
		log.Println("use legacy label names")
	}

	updateListeners(*urlPtr, *serverNamePtr, *waitPtr, *clockPtr, *legacyLabelPtr)

	http.Handle(*endpointPtr, promhttp.Handler())
	http.ListenAndServe(fmt.Sprintf(":%d", *portPtr), nil)
}
