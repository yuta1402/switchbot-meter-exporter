package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	"github.com/go-ble/ble/linux"
	"github.com/go-ble/ble/linux/hci/cmd"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "switchbot_meter"
)

type deviceStatus struct {
	Temperature    float64
	Humidity       int
	Battery        int
	LastUpdateTime time.Time
}

var deviceStatusByAddr = map[string]deviceStatus{}

func main() {
	var (
		addr = flag.String("listen-address", ":2112", "")
	)

	flag.Parse()

	d, err := dev.NewDevice("default")
	if err != nil {
		log.Fatalf("can't new device : %s", err)
	}
	ble.SetDefaultDevice(d)
	dev := d.(*linux.Device)

	if err := dev.HCI.Send(&cmd.LESetScanParameters{
		LEScanType:           0x01,   // 0x00: passive, 0x01: active
		LEScanInterval:       0x0004, // 0x0004 - 0x4000; N * 0.625msec
		LEScanWindow:         0x0004, // 0x0004 - 0x4000; N * 0.625msec
		OwnAddressType:       0x01,   // 0x00: public, 0x01: random
		ScanningFilterPolicy: 0x00,   // 0x00: accept all, 0x01: ignore non-white-listed.
	}, nil); err != nil {
		log.Fatal(err)
	}

	collector := newSwitchBotMeterCollector()
	prometheus.MustRegister(collector)

	ctx, cancel := context.WithCancel(context.Background())

	go ble.Scan(ctx, true, advHandler, nil)

	http.Handle("/metrics", promhttp.Handler())
	if err := http.ListenAndServe(*addr, nil); err != nil {
		cancel()
		log.Fatal(err)
	}
}

func advHandler(a ble.Advertisement) {
	found := false
	for _, uuid := range a.Services() {
		if uuid.String() == "cba20d00224d11e69fb80002a5d5c51b" {
			found = true
		}
	}
	if !found {
		return
	}

	for _, data := range a.ServiceData() {
		if len(data.Data) <= 0 {
			continue
		}

		// devType が SwitchBot MeterTH かどうかチェック
		// devType: https://github.com/OpenWonderLabs/python-host/wiki/Meter-BLE-open-API#Broadcast_Message
		devType := data.Data[0] & 0x7f
		if devType != 0x54 {
			continue
		}

		if len(data.Data) < 6 {
			continue
		}

		battery := int(data.Data[2])
		temp := float64(data.Data[4] & 0x7f)
		temp += float64(data.Data[3]) / 10
		humidity := int(data.Data[5] & 0x7f)

		log.Printf("[%s] temperature: %.1f, humidity: %d, battery: %d\n", a.Addr(), temp, humidity, battery)

		deviceStatusByAddr[a.Addr().String()] = deviceStatus{
			Temperature:    temp,
			Humidity:       humidity,
			Battery:        battery,
			LastUpdateTime: time.Now(),
		}
	}
}

type switchBotMeterCollector struct {
	tempGauge    *prometheus.GaugeVec
	humGauge     *prometheus.GaugeVec
	batteryGauge *prometheus.GaugeVec
}

func newSwitchBotMeterCollector() *switchBotMeterCollector {
	return &switchBotMeterCollector{
		tempGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "temperature",
		}, []string{"addr"}),
		humGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "humidity",
		}, []string{"addr"}),
		batteryGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "battery",
		}, []string{"addr"}),
	}
}

func (c *switchBotMeterCollector) Describe(ch chan<- *prometheus.Desc) {
	c.tempGauge.Describe(ch)
	c.humGauge.Describe(ch)
	c.batteryGauge.Describe(ch)
}

func (c *switchBotMeterCollector) Collect(ch chan<- prometheus.Metric) {
	for addr, status := range deviceStatusByAddr {
		c.tempGauge.WithLabelValues(addr).Set(status.Temperature)
		c.humGauge.WithLabelValues(addr).Set(float64(status.Humidity))
		c.batteryGauge.WithLabelValues(addr).Set(float64(status.Battery))
	}

	c.tempGauge.Collect(ch)
	c.humGauge.Collect(ch)
	c.batteryGauge.Collect(ch)
}
