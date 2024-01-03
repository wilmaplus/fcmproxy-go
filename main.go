package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	gokhttp "github.com/BRUHItsABunny/gOkHttp"
	firebase "github.com/BRUHItsABunny/go-android-firebase"
	firebase_api "github.com/BRUHItsABunny/go-android-firebase/api"
	andutils "github.com/BRUHItsABunny/go-android-utils"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"log"
	"net/http"
	"slices"
	"time"
)

var upgrader = websocket.Upgrader{} // use default options

var addr = flag.String("addr", "localhost:8082", "http service address")
var backwardCompat = flag.Bool("backward", false, "backwards compatibility config")

var persistentIds []string

var notificationQueue []NotificationPayload

var listenerStarted = false

var hub = newHub()

func GenerateNewDeviceAndAuth(appData *firebase_api.FirebaseAppData) *firebase_api.FirebaseDevice {
	ctx := context.Background()
	device := andutils.GetRandomDevice()

	fDevice := &firebase_api.FirebaseDevice{
		Device:                device,
		CheckinAndroidID:      0,
		CheckinSecurityToken:  0,
		GmsVersion:            "214218053",
		FirebaseClientVersion: "fcm-22.0.0",
	}

	hClient, err := gokhttp.TestHTTPClient()
	if err != nil {
		log.Fatal(err)
	}

	fClient := firebase.NewFirebaseClient(hClient, fDevice)
	_, err = fClient.NotifyInstallation(ctx, appData)
	if err != nil {
		log.Fatal(err)
	}

	time.Sleep(time.Second * 5)

	checkinResult, err := fClient.Checkin(ctx, appData, "", "")

	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(fmt.Sprintf("AndroidID (checkin): %d\nSecurityToken: %d", checkinResult.AndroidId, checkinResult.SecurityToken))

	result, err := fClient.C2DMRegisterAndroid(ctx, appData)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("notificationToken: \n", result)

	br, err := json.Marshal(&CredsPayload{Credentials: map[string]map[string]string{"fcm": {"token": result}}})
	if err != nil {
		fmt.Println(err)
	} else {
		hub.broadcast <- br
	}

	return fDevice
}

func GetDeviceInfo() *firebase_api.FirebaseDevice {
	var fDevice firebase_api.FirebaseDevice
	deviceJson, err := ioutil.ReadFile("device.json")
	if err != nil {
		log.Println(err)
		return nil
	}
	err = json.Unmarshal(deviceJson, &fDevice)
	if err != nil {
		log.Println(err)
		return nil
	}
	return &fDevice
}

func GetNotificationFromMsg(msg *firebase_api.DataMessageStanza) NotificationPayload {
	var data = map[string]string{}
	for _, value := range msg.AppData {
		data[value.GetKey()] = value.GetValue()
	}
	return NotificationPayload{
		Notification: Notification{
			Data: data,
		},
		PersistentId: *msg.PersistentId,
	}
}

func InitListener() {
	var appData firebase_api.FirebaseAppData
	appJson, _ := ioutil.ReadFile("config.json")
	err := json.Unmarshal(appJson, &appData)

	if err != nil {
		log.Println("Could not read config!")
		log.Fatal(err)
	}

	fDevice := GetDeviceInfo()
	if fDevice == nil {
		log.Println("Registering to FCM")
		fDevice = GenerateNewDeviceAndAuth(&appData)

		if *backwardCompat {
			// For backwards compatibility
			b, err := json.Marshal(map[string]map[string]string{"fcm": {"token": fDevice.FirebaseInstallations[appData.PackageID].NotificationData.NotificationToken}})
			if err != nil {
				fmt.Println(err)
				return
			}
			err = ioutil.WriteFile("fcm_cred.json", b, 0644)
			if err != nil {
				fmt.Println(err)
			}
		}

		b, err := json.Marshal(fDevice)
		if err != nil {
			fmt.Println(err)
			return
		}
		err = ioutil.WriteFile("device.json", b, 0644)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	client, err := gokhttp.NewHTTPClient()

	if err != nil {
		log.Fatal(err)
	}

	fClient := firebase.NewFirebaseClient(client, fDevice)

	err = fClient.MTalk.Connect()
	if err != nil {
		log.Fatal(err)
	}

	fClient.MTalk.OnNotification = func(notification *firebase_api.DataMessageStanza) {
		if slices.Contains(persistentIds, *notification.PersistentId) {
			// Already received, skip!
			return
		}
		persistentIds = append(persistentIds, *notification.PersistentId)
		if len(hub.clients) < 1 {
			notificationQueue = append(notificationQueue, GetNotificationFromMsg(notification))
			log.Println("Queued!")
		} else {
			convertedMsg := GetNotificationFromMsg(notification)
			br, err := json.Marshal(convertedMsg)
			log.Println(convertedMsg)
			if err != nil {
				fmt.Println(err)
				return
			}
			hub.broadcast <- br
		}
	}
	fmt.Println("Listening for messages")
	listenerStarted = true
}

func handleIncomingPersistentIds(receivedIds []string) {
	persistentIds = receivedIds
	log.Println("Received Persistent IDs!")
	if !listenerStarted {
		go InitListener()
	} else if len(notificationQueue) > 0 {
		log.Println("Clearing queue")
		for _, notification := range notificationQueue {
			b, err := json.Marshal(notification)
			if err != nil {
				fmt.Println(err)
				continue
			}
			hub.broadcast <- b
		}
		notificationQueue = nil
		log.Println("Queue cleared")
	}
}

func socket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}

	client := &Client{hub: hub, conn: c, send: make(chan []byte, 256)}
	hub.register <- client

	defer func() {
		hub.unregister <- client
		c.Close()
	}()
	go client.writePump()

	err = c.WriteJSON(&RequestPayload{Request: "get-persistent-ids"})
	if err != nil {
		log.Println(err)
		return
	}
	for {
		var response ResponsePayload
		err = c.ReadJSON(&response)
		if err != nil {
			log.Println("read:", err)
			break
		}
		if response.Success {
			log.Println(response)
			switch response.Request {
			case "get-persistent-ids":
				handleIncomingPersistentIds(response.Response)
			}
		} else {
			log.Println("Error: ", response)
		}
	}
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	http.HandleFunc("/", socket)
	go hub.run()
	log.Fatal(http.ListenAndServe(*addr, nil))
}
