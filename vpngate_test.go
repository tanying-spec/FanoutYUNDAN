package main

import "testing"

func TestParseNodeCSVAcceptsUnescapedQuoteInFeed(t *testing.T) {
	body := `*vpn_servers
#HostName,IP,CountryLong,CountryShort,Ping,Speed,NumVpnSessions,OpenVPN_ConfigData_Base64
vpn"dirty,192.0.2.10,United States,US,20,1000000,1,Y29uZmln
*`
	nodes, err := parseNodeCSV(body)
	if err != nil {
		t.Fatalf("parseNodeCSV returned error for tolerated feed row: %v", err)
	}
	if len(nodes) != 1 || nodes[0].IP != "192.0.2.10" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
}
