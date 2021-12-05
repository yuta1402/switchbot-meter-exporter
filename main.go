package main

import (
	"context"
	"log"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	"github.com/go-ble/ble/linux"
	"github.com/go-ble/ble/linux/hci/cmd"
)

type DeviceStatus struct {
	Temperature    float64
	Humidity       int
	Battery        int
	LastUpdateTime time.Time
}

var deviceStatusByAddr = map[string]DeviceStatus{}

func main() {
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

	ctx := context.Background()
	ble.Scan(ctx, true, advHandler, nil)
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

		deviceStatusByAddr[a.Addr().String()] = DeviceStatus{
			Temperature:    temp,
			Humidity:       humidity,
			Battery:        battery,
			LastUpdateTime: time.Now(),
		}
	}
}
