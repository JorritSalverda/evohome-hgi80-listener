package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/rs/zerolog/log"
)

type MessageProcessor interface {
	IsValidMessage(rawmsg string) (bool, error)
	DecodeMessage(rawmsg string) (message Message)
	ProcessMessage(message Message)
	ProcessExternalSensorMessage(message Message)
	ProcessZoneNameMessage(message Message)
	ProcessScheduleSyncMessage(message Message)
	ProcessRelayHeatDemandMessage(message Message)
	ProcessZoneInfoMessage(message Message)
	ProcessOtherCommandMessage(message Message)
	ProcessDeviceInfoMessage(message Message)
	ProcessBatteryInfoMessage(message Message)
	ProcessDhwSettingsMessage(message Message)
	ProcessHeartbeatMessage(message Message)
	ProcessDhwTemperatureMessage(message Message)
	ProcessWindowStatusMessage(message Message)
	ProcessSyncMessage(message Message)
	ProcessDhwStateMessage(message Message)
	ProcessBindMessage(message Message)
	ProcessSetpointUfhMessage(message Message)
	ProcessSetpointMessage(message Message)
	ProcessSetpointOverrideMessage(message Message)
	ProcessControllerModeMessage(message Message)
	ProcessZoneTemperatureMessage(message Message)
	ProcessDateRequestMessage(message Message)
	ProcessZoneHeatDemandMessage(message Message)
	ProcessActuatorCheckReqMessage(message Message)
	ProcessActuatorStateMessage(message Message)
	ProcessUnknownMessage(message Message)
	SendCommand(f io.ReadWriteCloser, command Command)
}

type messageProcessorImpl struct {
	bigqueryClient BigQueryClient
	commandQueue   chan Command
}

func NewMessageProcessor(bigqueryClient BigQueryClient, commandQueue chan Command) MessageProcessor {
	return &messageProcessorImpl{
		bigqueryClient: bigqueryClient,
		commandQueue:   commandQueue,
	}
}

func (mp *messageProcessorImpl) IsValidMessage(rawmsg string) (bool, error) {

	// check if it matches the pattern to be expected from evohome
	return regexp.MatchString(`^\d{3} ( I| W|RQ|RP) --- \d{2}:\d{6} (--:------ |\d{2}:\d{6} ){2}[0-9a-fA-F]{4} \d{3}`, rawmsg)
}

func (mp *messageProcessorImpl) DecodeMessage(rawmsg string) (message Message) {

	// message type
	messageType := strings.TrimSpace(rawmsg[4:6])

	// source device
	source := rawmsg[11:20]
	sourceTypeCode := source[0:2]
	sourceType := deviceTypeMap[sourceTypeCode]
	sourceID := source[3:]
	if sourceType == "" {
		sourceType = "NA"
	}

	// destination device
	destination := rawmsg[21:30]
	if destination == "--:------" {
		destination = rawmsg[31:40]
	}
	destinationTypeCode := destination[0:2]
	destinationType := deviceTypeMap[destinationTypeCode]
	destinationID := destination[3:]
	if destinationType == "" {
		destinationType = "NA"
	}

	isBroadcast := source == destination

	// command
	commandCode := rawmsg[41:45]
	commandType := commandsMap[strings.ToUpper(commandCode)]
	if commandType == "" {
		commandType = "unknown"
	}

	// payload
	payloadLength, err := strconv.ParseInt(rawmsg[46:49], 10, 64)
	if err != nil {
		payloadLength = 0
	}
	payload := rawmsg[50:]

	return Message{
		rawmsg:              rawmsg,
		messageType:         messageType,
		sourceTypeCode:      sourceTypeCode,
		sourceType:          sourceType,
		sourceID:            sourceID,
		destinationTypeCode: destinationTypeCode,
		destinationType:     destinationType,
		destinationID:       destinationID,
		isBroadcast:         isBroadcast,
		commandCode:         commandCode,
		commandType:         commandType,
		payloadLength:       payloadLength,
		payload:             payload,
	}
}

func (mp *messageProcessorImpl) ProcessMessage(message Message) {

	switch message.commandType {
	case "external_sensor":
		mp.ProcessExternalSensorMessage(message)
	case "zone_name":
		mp.ProcessZoneNameMessage(message)
	case "schedule_sync":
		mp.ProcessScheduleSyncMessage(message)
	case "relay_heat_demand":
		mp.ProcessRelayHeatDemandMessage(message)
	case "zone_info":
		mp.ProcessZoneInfoMessage(message)
	case "other_command":
		mp.ProcessOtherCommandMessage(message)
	case "device_info":
		mp.ProcessDeviceInfoMessage(message)
	case "battery_info":
		mp.ProcessBatteryInfoMessage(message)
	case "dhw_settings":
		mp.ProcessDhwSettingsMessage(message)
	case "heartbeat":
		mp.ProcessHeartbeatMessage(message)
	case "dhw_temperature":
		mp.ProcessDhwTemperatureMessage(message)
	case "window_status":
		mp.ProcessWindowStatusMessage(message)
	case "sync":
		mp.ProcessSyncMessage(message)
	case "dhw_state":
		mp.ProcessDhwStateMessage(message)
	case "bind":
		mp.ProcessBindMessage(message)
	case "setpoint_ufh":
		mp.ProcessSetpointUfhMessage(message)
	case "setpoint":
		mp.ProcessSetpointMessage(message)
	case "setpoint_override":
		mp.ProcessSetpointOverrideMessage(message)
	case "controller_mode":
		mp.ProcessControllerModeMessage(message)
	case "zone_temperature":
		mp.ProcessZoneTemperatureMessage(message)
	case "date_request":
		mp.ProcessDateRequestMessage(message)
	case "zone_heat_demand":
		mp.ProcessZoneHeatDemandMessage(message)
	case "actuator_check_req":
		mp.ProcessActuatorCheckReqMessage(message)
	case "actuator_state":
		mp.ProcessActuatorStateMessage(message)
	default:
		mp.ProcessUnknownMessage(message)
	}
}

func (mp *messageProcessorImpl) ProcessExternalSensorMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessZoneNameMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType == "RP" && message.payloadLength == 22 {
		// 045 RP --- 01:160371 18:010057 --:------ 0004 022 06004C6F676565726B616D6572000000000000000000
		// first byte has zone id, second byte empty, remaining bytes the zone name

		zoneID, _ := strconv.ParseInt(message.payload[0:2], 16, 64)
		zoneName, err := hex.DecodeString(message.payload[4:])
		if err == nil {
			reg := regexp.MustCompile(`[^a-zA-Z ]+`)
			zoneNameString := string(zoneName)
			zoneNameString = reg.ReplaceAllString(zoneNameString, "")
			zoneNameString = strings.TrimSpace(zoneNameString)

			if zoneNameString != "" {
				zoneInfo, knownZone := zoneNames[zoneID]
				if knownZone {
					zoneInfo.Name = zoneNameString
				} else {
					zoneInfo = ZoneInfo{
						ID:   zoneID,
						Name: zoneNameString,
					}
				}
				zoneNames[zoneID] = zoneInfo

				log.Info().
					Str("_msg", message.rawmsg).
					Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
					Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
					Interface("zoneInfo", zoneInfo).
					Msg(message.commandType)
			} else {
				log.Info().
					Str("_msg", message.rawmsg).
					Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
					Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
					Msg(message.commandType)
			}
		} else {
			log.Warn().Err(err).Msgf("Retrieving name for zone %v failed, retrying...", zoneID)

			mp.commandQueue <- Command{
				messageType:   "RQ",
				commandName:   "zone_name",
				destinationID: *evohomeID,
				payload: &DefaultPayload{
					Values: []int{int(zoneID), 0},
				},
			}
		}

		return
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessScheduleSyncMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessRelayHeatDemandMessage(message Message) {
	mp.processHeatDemandMessage(message)
}

func (mp *messageProcessorImpl) ProcessZoneInfoMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType != "RQ" && message.payloadLength%3 == 0 {
		// > RQ --- 18:730 01:160371 --:------ 000A 001 00
		// 045 RP --- 01:160371 18:010057 --:------ 000A 006 001001F40DAC (single zone)

		for i := 0; i < int(2*message.payloadLength); i += 12 {

			// payload has blocks of 6 bytes, with zone id in byte 1 flags in byte 2, min in byte 3 and max in byte 4
			zoneID, _ := strconv.ParseInt(message.payload[i+0:i+2], 16, 64)
			//flags, _ := strconv.ParseInt(message.payload[i+2:i+4], 16, 64)
			minTemperature, _ := strconv.ParseInt(message.payload[i+4:i+8], 16, 64)
			minTemperatureDegrees := float64(minTemperature) / 100
			maxTemperature, _ := strconv.ParseInt(message.payload[i+8:i+12], 16, 64)
			maxTemperatureDegrees := float64(maxTemperature) / 100

			// update zoneinfo if exist
			zoneInfo, knownZone := zoneNames[zoneID]
			if knownZone {
				zoneInfo.MinTemperature = minTemperatureDegrees
				zoneInfo.MaxTemperature = maxTemperatureDegrees
			} else {
				zoneInfo = ZoneInfo{
					ID:             zoneID,
					MinTemperature: minTemperatureDegrees,
					MaxTemperature: maxTemperatureDegrees,
				}
			}
			zoneNames[zoneID] = zoneInfo

			log.Info().
				Str("_msg", message.rawmsg).
				Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
				Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
				Interface("zoneInfo", zoneInfo).
				Msg(message.commandType)
		}

		return
	}

	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessOtherCommandMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessDeviceInfoMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType == "RP" && message.payloadLength == 22 {
		// > RQ --- 18:730 01:160371 --:------ 0418 003 000000
		// 045 RP --- 01:160371 18:010057 --:------ 0418 022 004000B0040000000000AA12B2C77FFFFF7000000001

		addr, _ := strconv.ParseInt(message.payload[4:6], 16, 64)
		devNo, _ := strconv.ParseInt(message.payload[10:12], 16, 64)
		devType, _ := strconv.ParseInt(message.payload[0:2], 16, 64)
		deviceID, _ := strconv.ParseInt(message.payload[0:2], 38, 44)

		log.Info().
			Str("_msg", message.rawmsg).
			Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
			Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
			Int("addr", int(addr)).
			Int("devNo", int(devNo)).
			Int("devType", int(devType)).
			Int("deviceID", int(deviceID)).
			Msg(message.commandType)

		if deviceID != 0 {
			nextDeviceAddr := int(addr) + 1
			log.Info().Msgf("Queueing device_info command for device %v", nextDeviceAddr)
			mp.commandQueue <- Command{
				messageType:   "RQ",
				commandName:   "device_info",
				destinationID: *evohomeID,
				payload: &DefaultPayload{
					Values: []int{0, 0, nextDeviceAddr},
				},
			}
		}
		return
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessBatteryInfoMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessDhwSettingsMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessHeartbeatMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType == "RP" {
		// > 095 RQ --- 18:010057 01:160371 --:------ 10E0 001 00
		// 045 RP --- 01:160371 18:010057 --:------ 10E0 038 000002FF0163FFFFFFFF140B07E1010807DD45766F20436F6C6F720000000000000000000000
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessDhwTemperatureMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessWindowStatusMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessSyncMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessDhwStateMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessBindMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessSetpointUfhMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessSetpointMessage(message Message) {

	if message.sourceType == "CTL" && message.messageType != "RQ" && message.payloadLength%3 == 0 {
		// 045  I --- 01:160371 --:------ 01:160371 2309 018 00079E0105DC02076C0306A405076C0605DC

		for i := 0; i < int(2*message.payloadLength); i += 6 {

			// payload has blocks of 3 bytes, with zone id in byte 1 and temperature in 'centi' degrees celsius in byte 2 and 3

			zoneID, _ := strconv.ParseInt(message.payload[i+0:i+2], 16, 64)
			setpoint, _ := strconv.ParseInt(message.payload[i+2:i+6], 16, 64)
			setpointDegrees := float64(setpoint) / 100

			if setpointDegrees > 100 {
				// oops, something must be wrong; stop further processing
				log.Warn().
					Str("_msg", message.rawmsg).
					Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
					Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
					Str("commandType", message.commandType).
					Msgf("Zone setpoint %v is too high, not processing...", setpointDegrees)
			}

			// update zoneinfo if exist
			zoneInfo, knownZone := zoneNames[zoneID]
			if knownZone {
				// check if min and max temp are already known
				if zoneInfo.MinTemperature != 0 && zoneInfo.MaxTemperature != 0 {
					// check if the setpoint isn't outside of the min and max temperature range
					if setpointDegrees > zoneInfo.MinTemperature && setpointDegrees < zoneInfo.MaxTemperature {
						zoneInfo.Setpoint = setpointDegrees
					}
				} else {
					zoneInfo.Setpoint = setpointDegrees
				}
			} else {
				zoneInfo = ZoneInfo{
					ID:       zoneID,
					Setpoint: setpointDegrees,
				}
			}
			zoneNames[zoneID] = zoneInfo

			log.Info().
				Str("_msg", message.rawmsg).
				Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
				Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
				Interface("zoneInfo", zoneInfo).
				Msg(message.commandType)

			if zoneID >= 12 || zoneInfo.Name != "" {
				measurements := []BigQueryMeasurement{
					BigQueryMeasurement{
						MessageType:      message.messageType,
						CommandType:      message.commandType,
						SourceType:       message.sourceType,
						SourceID:         message.sourceID,
						DestinationType:  message.destinationType,
						DestinationID:    message.destinationID,
						Broadcast:        message.isBroadcast,
						ZoneID:           bigquery.NullInt64{Int64: zoneID, Valid: true},
						ZoneName:         bigquery.NullString{StringVal: zoneInfo.Name, Valid: knownZone && zoneInfo.Name != ""},
						DemandPercentage: bigquery.NullFloat64{Valid: false},
						Temperature:      bigquery.NullFloat64{Valid: false},
						Setpoint:         bigquery.NullFloat64{Float64: setpointDegrees, Valid: true},
						InsertedAt:       time.Now().UTC(),
					},
				}

				err := mp.bigqueryClient.InsertMeasurements(*bigqueryDataset, *bigqueryTable, measurements)
				if err != nil {
					log.Fatal().Err(err).Msg("Failed inserting measurements into bigquery table")
				}
			}
		}

		return
	}

	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessSetpointOverrideMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType == "RP" {
		// > RQ --- 18:730 01:160371 --:------ 2349 001 00
		// 045 RP --- 01:160371 18:010057 --:------ 2349 007 00079E00FFFFFF
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessControllerModeMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType == "RP" {
		// > RQ --- 18:730 01:160371 --:------ 2E04 001 FF
		// 045 RP --- 01:160371 18:010057 --:------ 2E04 008 00FFFFFFFFFFFF00
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessZoneTemperatureMessage(message Message) {
	if message.sourceType == "CTL" && message.messageType != "RQ" && message.payloadLength%3 == 0 {
		// RQ --- 18:730 01:160371 --:------ 30C9 001 00
		// 045 RP --- 01:160371 18:010057 --:------ 30C9 003 000824 (single zone)
		// 045  I --- 01:160371 --:------ 01:160371 30C9 018 00081A0107BF0207CA03082005086B060884 (all zones)

		for i := 0; i < int(2*message.payloadLength); i += 6 {

			// payload has blocks of 3 bytes, with zone id in byte 1 and temperature in 'centi' degrees celsius in byte 2 and 3

			zoneID, _ := strconv.ParseInt(message.payload[i+0:i+2], 16, 64)
			temperature, _ := strconv.ParseInt(message.payload[i+2:i+6], 16, 64)
			temperatureDegrees := float64(temperature) / 100

			if temperatureDegrees > 100 {
				// oops, something must be wrong; stop further processing
				log.Warn().
					Str("_msg", message.rawmsg).
					Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
					Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
					Str("commandType", message.commandType).
					Msgf("Zone temperature %v is too high, not processing...", temperatureDegrees)
			}

			// update zoneinfo if exist
			zoneInfo, knownZone := zoneNames[zoneID]
			if knownZone {
				zoneInfo.Temperature = temperatureDegrees
			} else {
				zoneInfo = ZoneInfo{
					ID:          zoneID,
					Temperature: temperatureDegrees,
				}
			}
			zoneNames[zoneID] = zoneInfo

			log.Info().
				Str("_msg", message.rawmsg).
				Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
				Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
				Interface("zoneInfo", zoneInfo).
				Msg(message.commandType)

			if zoneID >= 12 || zoneInfo.Name != "" {
				measurements := []BigQueryMeasurement{
					BigQueryMeasurement{
						MessageType:      message.messageType,
						CommandType:      message.commandType,
						SourceType:       message.sourceType,
						SourceID:         message.sourceID,
						DestinationType:  message.destinationType,
						DestinationID:    message.destinationID,
						Broadcast:        message.isBroadcast,
						ZoneID:           bigquery.NullInt64{Int64: zoneID, Valid: true},
						ZoneName:         bigquery.NullString{StringVal: zoneInfo.Name, Valid: knownZone && zoneInfo.Name != ""},
						DemandPercentage: bigquery.NullFloat64{Valid: false},
						Temperature:      bigquery.NullFloat64{Float64: temperatureDegrees, Valid: true},
						Setpoint:         bigquery.NullFloat64{Valid: false},
						InsertedAt:       time.Now().UTC(),
					},
				}

				err := mp.bigqueryClient.InsertMeasurements(*bigqueryDataset, *bigqueryTable, measurements)
				if err != nil {
					log.Fatal().Err(err).Msg("Failed inserting measurements into bigquery table")
				}
			}
		}

		return
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessDateRequestMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessZoneHeatDemandMessage(message Message) {
	mp.processHeatDemandMessage(message)
}

func (mp *messageProcessorImpl) ProcessActuatorCheckReqMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessActuatorStateMessage(message Message) {
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) ProcessUnknownMessage(message Message) {
	log.Info().
		Str("_msg", message.rawmsg).
		Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
		Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
		Msg(message.commandType)
}

func (mp *messageProcessorImpl) processHeatDemandMessage(message Message) {

	if message.payloadLength == 2 {
		// heat demand for zone
		zoneID, _ := strconv.ParseInt(message.payload[0:2], 16, 64)
		demand, _ := strconv.ParseInt(message.payload[2:4], 16, 64)
		demandPercentage := float64(demand) / 200 * 100

		// update zoneinfo if exist
		zoneInfo, knownZone := zoneNames[zoneID]
		if knownZone {
			zoneInfo.HeatDemand = demandPercentage
			zoneNames[zoneID] = zoneInfo
		} else {
			zoneInfo = ZoneInfo{
				ID:         zoneID,
				HeatDemand: demandPercentage,
			}
		}
		zoneNames[zoneID] = zoneInfo

		log.Info().
			Str("_msg", message.rawmsg).
			Str("source", fmt.Sprintf("%v:%v", message.sourceType, message.sourceID)).
			Str("target", fmt.Sprintf("%v:%v", message.destinationType, message.destinationID)).
			Interface("zoneInfo", zoneInfo).
			Msg(message.commandType)

		if zoneID >= 12 || zoneInfo.Name != "" {
			measurements := []BigQueryMeasurement{
				BigQueryMeasurement{
					MessageType:      message.messageType,
					CommandType:      message.commandType,
					SourceType:       message.sourceType,
					SourceID:         message.sourceID,
					DestinationType:  message.destinationType,
					DestinationID:    message.destinationID,
					Broadcast:        message.isBroadcast,
					ZoneID:           bigquery.NullInt64{Int64: zoneID, Valid: true},
					ZoneName:         bigquery.NullString{StringVal: zoneInfo.Name, Valid: knownZone && zoneInfo.Name != ""},
					DemandPercentage: bigquery.NullFloat64{Float64: demandPercentage, Valid: true},
					Temperature:      bigquery.NullFloat64{Valid: false},
					Setpoint:         bigquery.NullFloat64{Valid: false},
					InsertedAt:       time.Now().UTC(),
				},
			}

			err := mp.bigqueryClient.InsertMeasurements(*bigqueryDataset, *bigqueryTable, measurements)
			if err != nil {
				log.Fatal().Err(err).Msg("Failed inserting measurements into bigquery table")
			}
		}

		return
	}
	mp.ProcessUnknownMessage(message)
}

func (mp *messageProcessorImpl) SendCommand(f io.ReadWriteCloser, command Command) {

	messageType := command.messageType
	commandCode := reverseCommandsMap[command.commandName]
	source := "18:730"
	destination := command.destinationID
	if command.broadcast {
		destination = source
	}

	// set default payload
	if command.payload == nil {
		command.payload = DefaultPayload{}
	}

	payload := command.payload.GetPayloadHex()
	payloadLength := len(payload) / 2

	commandString := fmt.Sprintf("%v --- %v %v --:------ %v %03d %v", messageType, source, destination, commandCode, payloadLength, payload)
	if command.broadcast {
		commandString = fmt.Sprintf("%v --- %v --:------ %v %v %03d %v", messageType, source, destination, commandCode, payloadLength, payload)
	}

	log.Info().Str("_msg", commandString).Msgf("> %v", command.commandName)

	_, err := f.Write([]byte(commandString + "\r\n"))
	if err != nil {
		log.Error().Err(err).Msgf("Sending %v command failed", command.commandName)
	}

	// wait for serial port to stabilise
	time.Sleep(time.Duration(2) * time.Second)
}
