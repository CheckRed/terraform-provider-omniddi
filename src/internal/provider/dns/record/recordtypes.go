package record

import (
	"strings"
)

const (
	recordsPath   = "/records/"
	recordPathFmt = "/records/%d"

	actionCreateRecord = "create_record"
	actionUpdateRecord = "update_record"
	actionDeleteRecord = "delete_record"

	apexName = "@"

	typeMX  = "MX"
	typeSRV = "SRV"
	typeCAA = "CAA"
)

var supportedTypes = []string{
	"A", "AAAA", "AFSDB", "ANAME", "CAA", "CNAME", "DNAME", "DNSKEY", "DS", "EUI48", "EUI64",
	"HINFO", "HTTPS", "ISDN", "LOC", "MB", "MD", "MF", "MG", "MR", "MX", "NAPTR", "NS", "PTR",
	"PX", "RP", "RRSIG", "RT", "SOA", "SPF", "SRV", "SSHFP", "SVCB", "TLSA", "TXT", "URI", "X25",
}

var multiValueStringTypes = setOf("A", "AAAA", "NS", "PTR", "SPF", "TXT")

var singleValueStringTypes = setOf("CNAME", "DNAME", "DNSKEY", "EUI48", "EUI64", "LOC", "MB", "MD", "MF", "MG", "MR", "RRSIG", "SOA", "X25")

var objectShapedTypes = setOf("HTTPS", "ISDN", "PX", "RP", "RT", "SVCB")

var hostnameValueTypes = setOf("CNAME", "DNAME", "MB", "MD", "MF", "MG", "MR", "NS", "PTR")

func setOf(items ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

func isStringType(recordType string) bool {
	_, multi := multiValueStringTypes[recordType]
	_, single := singleValueStringTypes[recordType]
	return multi || single
}

func isSingleValue(recordType string) bool {
	_, single := singleValueStringTypes[recordType]
	return single
}

func supportsDisabledValues(recordType string) bool {
	_, ok := multiValueStringTypes[recordType]
	return ok
}

func typedBlockFor(recordType string) string {
	switch recordType {
	case typeMX:
		return "mx"
	case typeSRV:
		return "srv"
	case typeCAA:
		return "caa"
	}
	return ""
}

func allowedFormsDescription(recordType string) string {
	switch {
	case isStringType(recordType):
		return "values"
	case typedBlockFor(recordType) != "":
		return "`" + typedBlockFor(recordType) + "` or `data_json`"
	}
	return "data_json"
}

func normalizeName(name string) string {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return apexName
	}
	return trimmed
}
