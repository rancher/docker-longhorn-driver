package model

type Volume struct {
	Name       string `json:"name"`
	Mountpoint string
	Opts       map[string]string
}
