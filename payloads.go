package main

type RequestPayload struct {
	Request string `json:"request"`
}

type ResponsePayload struct {
	Request  string   `json:"request"`
	Success  bool     `json:"success"`
	Response []string `json:"response"`
}

type Notification struct {
	Data map[string]string `json:"data"`
}

type NotificationPayload struct {
	Notification Notification `json:"notification"`
	PersistentId string       `json:"persistentId"`
}

type CredsPayload struct {
	Credentials interface{} `json:"credentials"`
}
