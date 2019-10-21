// Copyright 2019 Infostellar, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"github.com/stellarstation-api/examples/go/stream_benchmark/conn"
	"log"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc"

	stellarstation "github.com/infostellarinc/go-stellarstation/api/v1"
)

type Metric struct {
	timeFirstByteReceived time.Time
	timeLastByteReceived  time.Time
	dataSize              int64
	metricTime            time.Time
}

type MetricsData struct {
	initialTime                    time.Time
	metrics                        []*Metric
	mostRecentTimeLastByteReceived time.Time
	mu                             sync.Mutex
}

type OutputDetails struct {
	now                            time.Time
	mostRecentTimeLastByteReceived time.Time
	averageFirstByteLatency        float64
	averageLastByteLatency         float64
	totalDataSize                  int64
	metricsCount                   int
	averageDataSize                float64
	mbps                           float64
	outOfOrderData                 int
}

func NewMetric(telemetry *stellarstation.Telemetry) *Metric {
	firstByteReceived, err := ptypes.Timestamp(telemetry.TimeFirstByteReceived)
	if err != nil {
		log.Fatal(err)
	}
	lastByteReceived, err := ptypes.Timestamp(telemetry.TimeLastByteReceived)
	if err != nil {
		log.Fatal(err)
	}

	return &Metric{
		timeFirstByteReceived: firstByteReceived,
		timeLastByteReceived:  lastByteReceived,
		dataSize:              int64(len(telemetry.Data)),
		metricTime:            time.Now(),
	}
}

func NewMetricsData() *MetricsData {
	return &MetricsData{
		initialTime:                    time.Now(),
		mostRecentTimeLastByteReceived: time.Time{},
	}
}

func (d *MetricsData) add(metric *Metric) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.metrics = append(d.metrics, metric)
}

func (d *MetricsData) compileSummary() OutputDetails {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	outputDetails := d.compileDetails(d.initialTime)
	d.mostRecentTimeLastByteReceived = outputDetails.mostRecentTimeLastByteReceived

	d.initialTime = now
	d.metrics = make([]*Metric, 0)
	return outputDetails
}

func (d *MetricsData) compileDetails(initialTime time.Time) OutputDetails {
	now := time.Now()

	var totalFirstByteLatency time.Duration
	var totalLastByteLatency time.Duration
	var totalDataSize int64

	outOfOrderData := 0
	previousFirstByteTime := time.Time{}
	mostRecentTimeLastByteReceived := d.mostRecentTimeLastByteReceived

	for _, metric := range d.metrics {
		firstByteLatency := metric.metricTime.Sub(metric.timeFirstByteReceived)
		lastByteLatency := metric.metricTime.Sub(metric.timeLastByteReceived)

		totalFirstByteLatency += firstByteLatency
		totalLastByteLatency += lastByteLatency
		totalDataSize += metric.dataSize

		if metric.timeLastByteReceived.After(mostRecentTimeLastByteReceived) {
			mostRecentTimeLastByteReceived = metric.timeLastByteReceived
		}

		if previousFirstByteTime.After(metric.timeFirstByteReceived) {
			outOfOrderData += 1
		}
		previousFirstByteTime = metric.timeFirstByteReceived

	}

	var averageFirstByteLatency float64
	var averageLastByteLatency float64
	var averageDataSize float64

	if len(d.metrics) > 0 {
		averageFirstByteLatency = totalFirstByteLatency.Seconds() / float64(len(d.metrics))
		averageLastByteLatency = totalLastByteLatency.Seconds() / float64(len(d.metrics))
		averageDataSize = float64(totalDataSize / (int64(len(d.metrics))))
	}

	mbps := float64(totalDataSize) / (now.Sub(initialTime).Seconds()) * 8 / 1024 / 1024

	outputDetails := OutputDetails{
		now:                            time.Now(),
		mostRecentTimeLastByteReceived: mostRecentTimeLastByteReceived,
		averageFirstByteLatency:        averageFirstByteLatency,
		averageLastByteLatency:         averageLastByteLatency,
		totalDataSize:                  totalDataSize,
		metricsCount:                   len(d.metrics),
		averageDataSize:                averageDataSize,
		mbps:                           mbps,
		outOfOrderData:                 outOfOrderData,
	}

	return outputDetails
}

func (o OutputDetails) Print() {
	format := "2006-01-02T15:04:05.999"
	outLine := fmt.Sprintf("%v\t%v\t%.3f\t%.3f\t%v\t%v\t%v\t%.6f\t%v\t",
		o.now.Format(format),
		o.mostRecentTimeLastByteReceived.Format(format),
		o.averageFirstByteLatency,
		o.averageLastByteLatency,
		o.totalDataSize,
		o.metricsCount,
		o.averageDataSize,
		o.mbps,
		o.outOfOrderData)
	fmt.Println(outLine)
}

func openStream(satelliteId string, conn *grpc.ClientConn) (stellarstation.StellarStationService_OpenSatelliteStreamClient, error) {
	client := stellarstation.NewStellarStationServiceClient(conn)

	stream, err := client.OpenSatelliteStream(context.Background())
	if err != nil {
		return nil, err
	}

	req := stellarstation.SatelliteStreamRequest{
		AcceptedFraming: []stellarstation.Framing{stellarstation.Framing_BITSTREAM},
		SatelliteId:     satelliteId,
		StreamId:        "",
	}

	err = stream.Send(&req)
	if err != nil {
		return nil, err
	}

	return stream, nil
}

func run() {
	conn, err := conn.Dial()
	if err != nil {
		log.Fatal(err)
	}

	stream, err := openStream("99", conn)
	if err != nil {
		log.Fatal(err)
	}

	// Initialize variables
	metricsData := MetricsData{}
	gotFirstTime := false

	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				metricsData.compileSummary().Print()
			}
		}
	}()

	//timer = RepeatTimer(int(interval), print_summary, args=(metricsData,))
	fmt.Println("Listening for messages")

	for {
		res, err := stream.Recv()
		if err != nil {
			log.Fatal(err)
		}

		if !gotFirstTime {
			gotFirstTime = true
			fmt.Println("Receiving messages")
		}

		switch res.Response.(type) {
		case *stellarstation.SatelliteStreamResponse_ReceiveTelemetryResponse:
			telemetry := res.GetReceiveTelemetryResponse().Telemetry
			metricsData.add(NewMetric(telemetry))
		}
	}
}

func main() {

	//     parser = argparse.ArgumentParser()
	//     parser.add_argument("-int",
	//                         "--interval",
	//                         help="reporting interval in seconds",
	//                         default="10")
	//     parser.add_argument("-i",
	//                         "--id",
	//                         help="satellite id",
	//                         default="5")
	//     parser.add_argument("-k",
	//                         "--key",
	//                         help="API key file",
	//                         default="stellarstation-private-key.json")
	//     parser.add_argument("-e",
	//                         "--endpoint",
	//                         help="API endpoint",
	//                         default="api.stellarstation.com:443")
	//     parser.add_argument("-d",
	//                         "--directory",
	//                         help="output directory",
	//                         default="")
	//     args = parser.parse_args()
	//
	//     # Load the private key downloaded from the StellarStation Console.
	//     credentials = google_auth_jwt.Credentials.from_service_account_file(
	//         args.key,
	//         audience="https://api.stellarstation.com")
	//
	header := "DATE\tMost recent received\tAvg seconds to last byte\t" +
		"Avg seconds to first byte\tTotal bytes\tNum messages\tAvg bytes\tMbps\tOut of order count"
	fmt.Println(header)
	run()

	//     try:
	//         printer.print_header()
	//         run(credentials, args.endpoint, args.id, args.interval, printer.print_summary)
	//     finally:
	//         printer.close()
}
