package zone

import (
	"strings"
)

type primaryServerRemote struct {
	IP     string
	Port   int64
	Secure bool
}

type zoneRemote struct {
	TTL, Refresh, Retry, Expiry, Minimum, SOASerial *int64
	Email, PrimaryNameServer, Policy                *string
	Protocol, CIDR                                  *string
	EnableDynamicDNS, EnableDNSSEC                  *bool
	AutoCreatePTRRecords, EnableZoneTransfer        *bool
	SecureSync                                      *bool
	PrimaryServers                                  []primaryServerRemote
	Nameservers                                     []string
	BusinessUnitIDs                                 []int64
}

func asInt64(raw any) *int64 {
	switch v := raw.(type) {
	case float64:
		n := int64(v)
		return &n
	case int64:
		return &v
	}
	return nil
}

func asBool(raw any) *bool {
	if v, ok := raw.(bool); ok {
		return &v
	}
	return nil
}

func asString(raw any) *string {
	if v, ok := raw.(string); ok && v != "" {
		return &v
	}
	return nil
}

func firstInt(values ...*int64) *int64 {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func blockEnable(cfg map[string]any, block, flat string) *bool {
	switch v := cfg[block].(type) {
	case map[string]any:
		if enabled := asBool(v["enable"]); enabled != nil {
			return enabled
		}
	case bool:
		return &v
	}
	return asBool(cfg[flat])
}

func endpointIP(raw any) (string, int64, bool, bool) {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v), defaultDNSPort, false, v != ""
	case map[string]any:
		ip := asString(v["ip"])
		if ip == nil {
			ip = asString(v["serverName"])
		}
		if ip == nil {
			return "", 0, false, false
		}
		port := firstInt(asInt64(v["port"]))
		if port == nil {
			p := int64(defaultDNSPort)
			port = &p
		}
		secure := asBool(v["secure_sync"])
		return *ip, *port, secure != nil && *secure, true
	}
	return "", 0, false, false
}

func parseZone(dto *zoneDTO) zoneRemote {
	cfg := dto.ConfigJSON
	if cfg == nil {
		cfg = map[string]any{}
	}
	soa := dto.SOA
	if soa == nil {
		soa = &soaDTO{}
	}
	r := zoneRemote{
		TTL:                  firstInt(soa.TTL.int64(), asInt64(cfg["ttl"])),
		Refresh:              firstInt(soa.Refresh.int64(), asInt64(cfg["refresh"])),
		Retry:                firstInt(soa.Retry.int64(), asInt64(cfg["retry"])),
		Expiry:               firstInt(soa.Expire.int64(), asInt64(cfg["expiry"])),
		Minimum:              firstInt(soa.Nttl.int64(), asInt64(cfg["minimum"])),
		SOASerial:            soa.Serial.int64(),
		Email:                asString(cfg["email"]),
		PrimaryNameServer:    soa.PrimaryNameServer,
		Policy:               asString(cfg["policy"]),
		Protocol:             asString(cfg["protocol"]),
		CIDR:                 asString(cfg["cidr"]),
		EnableDynamicDNS:     blockEnable(cfg, "dynamic_dns", "enable_dynamic_dns"),
		EnableDNSSEC:         blockEnable(cfg, "dnssec", "enable_dnssec"),
		AutoCreatePTRRecords: asBool(cfg["auto_create_ptr_records"]),
		EnableZoneTransfer:   blockEnable(cfg, "zone_transfer", "enable_zone_transfer"),
		SecureSync:           asBool(cfg["secure_sync"]),
	}
	if servers, ok := cfg["primary_servers"].([]any); ok {
		for _, raw := range servers {
			if ip, port, secure, ok := endpointIP(raw); ok {
				r.PrimaryServers = append(r.PrimaryServers, primaryServerRemote{IP: ip, Port: port, Secure: secure})
			}
		}
	}
	if servers, ok := cfg["nameservers"].([]any); ok {
		for _, raw := range servers {
			if ip, _, _, ok := endpointIP(raw); ok {
				r.Nameservers = append(r.Nameservers, ip)
			}
		}
	}
	if r.SecureSync == nil {
		for _, p := range r.PrimaryServers {
			if p.Secure {
				yes := true
				r.SecureSync = &yes
			}
		}
	}
	r.BusinessUnitIDs = businessUnitOrder(dto)
	return r
}

func businessUnitOrder(dto *zoneDTO) []int64 {
	ids := make([]int64, 0, len(dto.BusinessUnits))
	if dto.PrimaryBusinessUnit != nil {
		ids = append(ids, dto.PrimaryBusinessUnit.ID)
	}
	for _, bu := range dto.BusinessUnits {
		if dto.PrimaryBusinessUnit == nil || bu.ID != dto.PrimaryBusinessUnit.ID {
			ids = append(ids, bu.ID)
		}
	}
	return ids
}
