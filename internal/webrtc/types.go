package webrtc

type Capability struct {
	Mode             string   `json:"mode"`
	STUNServers      []string `json:"stunServers"`
	TURNServers      []string `json:"turnServers"`
	RelayRequired    bool     `json:"relayRequired"`
	FallbackStrategy string   `json:"fallbackStrategy"`
}

type SignalEnvelope struct {
	Type string      `json:"type"`
	Body interface{} `json:"body"`
}
