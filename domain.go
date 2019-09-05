package main

// State contains the state of all devices emitting data catched by the HGI80 device
type State struct {
}

type Message struct {
	SourceID       string
	MessageType    string
	Source         string
	SourceType     string
	SourceTypeName string
	CommandCode    string
	CommandName    string

	// self.source_id    = rawmsg[11:20]

	// self.msg_type     = rawmsg[4:6].strip()
	// self.source       = rawmsg[11:20]               # device 1 - This looks as if this is always the source; Note this is overwritten with name of device
	// self.source_type  = rawmsg[11:13]               # the first 2 digits seem to be identifier for type of device
	// self.source_name  = self.source
	// self.device2      = rawmsg[21:30]               # device 2 - Requests (RQ), Responses (RP) and Write (W) seem to be sent to device 2 only
	// self.device2_type = rawmsg[21:23]               # device 2 type
	// self.device2_name = self.device2
	// self.device3      = rawmsg[31:40]               # device 3 - Information (I) is to device 3. Broadcast messages have device 1 and 3 are the same
	// self.device3_type = rawmsg[31:33]               # device 3 type
	// self.device3_name = self.device3

	// if self.device2 == EMPTY_DEVICE_ID:
	//     self.destination = self.device3
	//     self.destination_type = self.device3_type
	// else:
	//     self.destination = self.device2
	//     self.destination_type = self.device2_type
	// self.destination_name = self.destination
	// self._initialise_device_names()

	// self.command_code = rawmsg[41:45].upper()       # command code hex
	// self.command_name = self.command_code           # needs to be assigned outside, as we are doing all the processing outside of this class/struct
	// try:
	//     self.payload_length = int(rawmsg[46:49])          # Note this is not HEX...
	// except Exception as e:
	//     print ("Error instantiating Message class on line "{}": {}. Raw msg: "{}". length = {}".format(sys.exc_info()[-1].tb_lineno, str(e), rawmsg, len(rawmsg)))
	//     self.payload_length = 0

	// self.payload      = rawmsg[50:]
	// self.port         = None
	// self.failed_decrypt= "_ENC" in rawmsg or "_BAD" in rawmsg or "BAD_" in rawmsg or "ERR" in rawmsg
}

var commandsMap = map[string]string{
	"0002": "external_sensor",
	"0004": "zone_name",
	"0006": "schedule_sync",
	"0008": "relay_heat_demand",
	"000a": "zone_info",
	"0100": "other_command",
	"0418": "device_info",
	"1060": "battery_info",
	"10a0": "dhw_settings",
	"10e0": "heartbeat",
	"1260": "dhw_temperature",
	"12b0": "window_status",
	"1f09": "sync",
	"1f41": "dhw_state",
	"1fc9": "bind",
	"22c9": "setpoint_ufh",
	"2309": "setpoint",
	"2349": "setpoint_override",
	"2e04": "controller_mode",
	"30c9": "zone_temperature",
	"313f": "date_request",
	"3150": "zone_heat_demand",
	"3b00": "actuator_check_req",
	"3ef0": "actuator_state",
}

var deviceTypeMap = map[string]string{
	"01": "CTL",
	"02": "UFH",
	"04": "TRV",
	"07": "DHW",
	"13": "BDR",
	"30": "GWAY",
	"34": "STAT",
}
