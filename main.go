package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	"github.com/go-ble/ble/linux"
	"github.com/go-ble/ble/linux/hci/cmd"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "switchbot_meter"

	// https://github.com/OpenWonderLabs/SwitchBotAPI-BLE?tab=readme-ov-file#uuid-update-notes
	SwitchBotServiceDataUUID = "fd3d"
)

type meterDeviceStatus struct {
	Temperature float64
	Humidity    int
	Battery     int
}

type hub2DeviceStatus struct {
	Temperature float64
	Humidity    int
}

var meterDeviceStatusByAddr = map[string]meterDeviceStatus{}
var hub2DeviceStatusByAddr = map[string]hub2DeviceStatus{}

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
	for _, data := range a.ServiceData() {
		if data.UUID.String() != SwitchBotServiceDataUUID {
			continue
		}

		if len(data.Data) <= 0 {
			continue
		}

		// devType: https://github.com/OpenWonderLabs/python-host/wiki/Meter-BLE-open-API#Broadcast_Message
		devType := data.Data[0] & 0x7f

		switch devType {
		case 0x54:
			updateMeterDeviceStatus(a.Addr().String(), data.Data)
		case 0x76:
			updateHub2DeviceStatus(a.Addr().String(), a.ManufacturerData())
		default:
		}
	}
}

func updateHub2DeviceStatus(addr string, data []byte) {
	if len(data) < 18 {
		return
	}

	temp := float64(data[15]&0xf)/10 + float64(data[16]&0x7f)
	above_freezing := data[16] & 0x80
	if above_freezing == 0 {
		temp = -temp
	}

	humidity := int(data[17] & 0x7f)

	log.Printf("[%s] Hub2, temp: %.1f, humidity: %d", addr, temp, humidity)

	hub2DeviceStatusByAddr[addr] = hub2DeviceStatus{
		Temperature: temp,
		Humidity:    humidity,
	}
}

func updateMeterDeviceStatus(addr string, data []byte) {
	if len(data) < 6 {
		return
	}

	battery := int(data[2])
	temp := float64(data[4] & 0x7f)
	temp += float64(data[3]) / 10
	humidity := int(data[5] & 0x7f)

	log.Printf("[%s] Meter, temp: %.1f, humidity: %d, battery: %d\n", addr, temp, humidity, battery)

	meterDeviceStatusByAddr[addr] = meterDeviceStatus{
		Temperature: temp,
		Humidity:    humidity,
		Battery:     battery,
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
	for addr, status := range meterDeviceStatusByAddr {
		c.tempGauge.WithLabelValues(addr).Set(status.Temperature)
		c.humGauge.WithLabelValues(addr).Set(float64(status.Humidity))
		c.batteryGauge.WithLabelValues(addr).Set(float64(status.Battery))
	}

	for addr, status := range hub2DeviceStatusByAddr {
		c.tempGauge.WithLabelValues(addr).Set(status.Temperature)
		c.humGauge.WithLabelValues(addr).Set(float64(status.Humidity))
	}

	c.tempGauge.Collect(ch)
	c.humGauge.Collect(ch)
	c.batteryGauge.Collect(ch)
}
