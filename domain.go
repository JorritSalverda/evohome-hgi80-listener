package main

import (
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
)

var commandsMap = map[string]string{
	"0002": "external_sensor",
	"0004": "zone_name",
	"0006": "schedule_sync",
	"0008": "relay_heat_demand",
	"000A": "zone_info",
	"0100": "other_command",
	"0418": "device_info",
	"1060": "battery_info",
	"10A0": "dhw_settings",
	"10E0": "heartbeat",
	"1260": "dhw_temperature",
	"12B0": "window_status",
	"1F09": "sync",
	"1F41": "dhw_state",
	"1FC9": "bind",
	"22C9": "setpoint_ufh",
	"2309": "setpoint",
	"2349": "setpoint_override",
	"2E04": "controller_mode",
	"30C9": "zone_temperature",
	"313F": "date_request",
	"3150": "zone_heat_demand",
	"3B00": "actuator_check_req",
	"3EF0": "actuator_state",
}

var reverseCommandsMap = reverseMap(commandsMap)

var deviceTypeMap = map[string]string{
	"01": "CTL",  // controller (evohome touch)
	"02": "UFH",  // underfloor heating (HCE80)
	"04": "TRV",  // thermostatic radiator valve
	"07": "DHW",  // domestic hot water
	"10": "OTB",  // opentherm bridge (R8810A1018)
	"13": "BDR",  // on/off relay (BDR91)
	"30": "GWAY", // remote gateway
	"34": "STAT", // thermostat
}

type BigQueryMeasurement struct {
	MessageType      string               `bigquery:"message_type"`
	CommandType      string               `bigquery:"command_type"`
	SourceType       string               `bigquery:"source_type"`
	SourceID         string               `bigquery:"source_id"`
	DestinationType  string               `bigquery:"destination_type"`
	DestinationID    string               `bigquery:"destination_id"`
	Broadcast        bool                 `bigquery:"broadcast"`
	ZoneID           bigquery.NullInt64   `bigquery:"zone_id"`
	ZoneName         bigquery.NullString  `bigquery:"zone_name"`
	DemandPercentage bigquery.NullFloat64 `bigquery:"demand_percentage"`
	Temperature      bigquery.NullFloat64 `bigquery:"temperature"`
	Setpoint         bigquery.NullFloat64 `bigquery:"setpoint"`
	InsertedAt       time.Time            `bigquery:"inserted_at"`
}

type Command struct {
	messageType   string
	commandName   string
	broadcast     bool
	destinationID string
	payload       Payload
}

type Payload interface {
	GetPayloadHex() string
}

type DefaultPayload struct {
	Values []int
}

func (p DefaultPayload) GetPayloadHex() string {

	// make sure there's at least one value
	if len(p.Values) == 0 {
		p.Values = []int{0}
	}

	payload := ""
	for _, v := range p.Values {
		payload += fmt.Sprintf("%02X", v)
	}

	return payload
}

type Message struct {
	rawmsg        string
	messageType   string
	source        string
	destination   string
	command       string
	payloadLength int64
	payload       string
}

func (m Message) GetSourceTypeCode() string {
	return m.source[0:2]
}

func (m Message) GetSourceTypeName() string {
	sourceTypeName := deviceTypeMap[m.GetSourceTypeCode()]
	if sourceTypeName == "" {
		sourceTypeName = "NA"
	}
	return sourceTypeName
}

func (m Message) GetSourceID() string {
	return m.destination[3:]
}

func (m Message) GetDestinationTypeCode() string {
	return m.destination[0:2]
}

func (m Message) GetDestinationTypeName() string {
	destinationTypeName := deviceTypeMap[m.GetDestinationTypeCode()]
	if destinationTypeName == "" {
		destinationTypeName = "NA"
	}
	return destinationTypeName
}

func (m Message) GetDestinationID() string {
	return m.source[3:]
}

func (m Message) GetCommandName() string {
	commandName := commandsMap[strings.ToUpper(m.command)]
	if commandName == "" {
		commandName = "unknown"
	}
	return commandName
}

func (m Message) IsBroadcast() bool {
	return m.source == m.destination
}

type State struct {
	ZoneInfoMap map[int64]ZoneInfo
	LastUpdated time.Time
}

type ZoneInfo struct {
	ID             int64
	Name           string
	MinTemperature float64
	MaxTemperature float64
	Temperature    float64
	Setpoint       float64
	HeatDemand     float64
}

func (z ZoneInfo) IsActualZone() bool {
	return z.ID < 12 && z.Name != ""
}

type BigQueryZone struct {
	ZoneID      int64                `bigquery:"zone_id"`
	ZoneName    string               `bigquery:"zone_name"`
	Temperature bigquery.NullFloat64 `bigquery:"temperature"`
	Setpoint    bigquery.NullFloat64 `bigquery:"setpoint"`
	HeatDemand  bigquery.NullFloat64 `bigquery:"heat_demand"`
}
