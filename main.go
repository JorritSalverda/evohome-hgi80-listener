package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/jacobsa/go-serial/serial"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// set when building the application
	app       string
	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()

	// application specific config
	stateFilePath = kingpin.Flag("state-file-path", "Path to file with state.").Default("/configs/state.json").OverrideDefaultFromEnvar("STATE_FILE_PATH").String()
	hgiDevicePath = kingpin.Flag("hgi-device-path", "Path to usb device connecting HGI80.").Default("/dev/ttyUSB0").OverrideDefaultFromEnvar("HGI_DEVICE_PATH").String()
	evohomeID     = kingpin.Flag("evohome-id", "ID of the Evohome Touch device").Envar("EVOHOME_ID").Required().String()
	namespace     = kingpin.Flag("namespace", "Namespace the pod runs in.").Envar("NAMESPACE").Required().String()
)

func main() {

	// parse command line parameters
	kingpin.Parse()

	// log as severity for stackdriver logging to recognize the level
	zerolog.LevelFieldName = "severity"

	// set some default fields added to all logs
	// log.Logger = zerolog.New(os.Stdout).With().
	// 	Timestamp().
	// 	Str("app", app).
	// 	Str("version", version).
	// 	Logger()

	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	output.FormatTimestamp = func(i interface{}) string {
		return ""
	}
	output.FormatLevel = func(i interface{}) string {
		// return strings.ToUpper(fmt.Sprintf("| %-6s|", i))
		return ""
	}
	output.FormatMessage = func(i interface{}) string {
		return fmt.Sprintf("%s", i)
	}
	output.FormatFieldName = func(i interface{}) string {
		return fmt.Sprintf("| %s:", i)
	}
	output.FormatFieldValue = func(i interface{}) string {
		return strings.ToUpper(fmt.Sprintf("%s", i))
	}

	log.Logger = zerolog.New(output).With().Timestamp().Logger()

	// use zerolog for any logs sent via standard log library
	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

	// log startup message
	log.Info().
		Str("branch", branch).
		Str("revision", revision).
		Str("buildDate", buildDate).
		Str("goVersion", goVersion).
		Msgf("Starting %v version %v...", app, version)

	// check if state file exists in configmap
	var state State
	if _, err := os.Stat(*stateFilePath); !os.IsNotExist(err) {

		log.Info().Msgf("File %v exists, reading contents...", *stateFilePath)

		// read state file
		data, err := ioutil.ReadFile(*stateFilePath)
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed reading file from path %v", *stateFilePath)
		}

		log.Info().Msgf("Unmarshalling file %v contents...", *stateFilePath)

		// unmarshal state file
		if err := json.Unmarshal(data, &state); err != nil {
			log.Fatal().Err(err).Interface("data", data).Msg("Failed unmarshalling state")
		}
	}

	log.Info().Msgf("Listening to serial usb device at %v for messages from evohome touch device with id %v...", *hgiDevicePath, *evohomeID)

	options := serial.OpenOptions{
		PortName:               *hgiDevicePath,
		BaudRate:               115200,
		DataBits:               8,
		StopBits:               1,
		MinimumReadSize:        0,
		InterCharacterTimeout:  100,
		ParityMode:             serial.PARITY_NONE,
		Rs485Enable:            false,
		Rs485RtsHighDuringSend: false,
		Rs485RtsHighAfterSend:  false,
	}

	f, err := serial.Open(options)
	if err != nil {
		log.Fatal().Err(err).Interface("options", options).Msg("Failed opening serial device")
	}
	defer f.Close()

	in := bufio.NewReader(f)

	for {

		// buf := make([]byte, 128)
		// n, err := f.Read(buf)

		buf, err := in.ReadBytes('\n')

		if err != nil {
			if err != io.EOF {
				log.Warn().Err(err).Msg("Error reading from serial port, closing port...")
				f.Close()

				log.Info().Msg("Sleeping for 5 seconds...")
				time.Sleep(5 * time.Second)

				log.Info().Msgf("Listening to serial usb device at %v for messages from evohome touch device with id %v...", *hgiDevicePath, *evohomeID)
				f, err = serial.Open(options)
				if err != nil {
					log.Fatal().Err(err).Interface("options", options).Msg("Failed opening serial device")
				}
				defer f.Close()
			}
		} else {
			// buf = buf[:n]

			//rawmsg := hex.EncodeToString(buf)
			rawmsg := strings.TrimSpace(string(buf))

			length := len(rawmsg)

			// Make sure no obvious errors in getting the data....
			if length > 10 &&
				!strings.Contains(rawmsg, "_ENC") &&
				!strings.Contains(rawmsg, "_BAD") &&
				!strings.Contains(rawmsg, "BAD") &&
				!strings.Contains(rawmsg, "ERR") {

				// Echos of commands sent by us come back without the --- prefix. Noticed on the fifo firmware that sometimes the request type prefix seems to be messed up. Workaround for this...
				// if !strings.HasPrefix(rawmsg, "---") {
				// 	if strings.HasPrefix(rawmsg, "W---") {
				// 		rawmsg = rawmsg[1:]
				// 	} else {
				// 		rawmsgparts := strings.Split(rawmsg, "")
				// 		if len(rawmsgparts[0]) < 2 {
				// 			rawmsg = "---  " + rawmsg
				// 		} else {
				// 			rawmsg = "--- " + rawmsg
				// 		}
				// 	}
				// }

				// check if it matches the pattern to be expected from evohome
				match, _ := regexp.MatchString(`^\d{3} ( I| W|RQ|RP) ---`, rawmsg)
				//match, _ := regexp.MatchString(`^\d{3} ( I| W|RQ|RP) --- \d{2}:\d{6} (--:------ |\d{2}:\d{6} ){2}[0-9a-fA-F]{4} \d{3}`, rawmsg)
				length := len(rawmsg)

				if match {
					message := Message{
						SourceID:       rawmsg[11:20],
						MessageType:    strings.Trim(rawmsg[4:6], " "),
						Source:         rawmsg[11:20],
						SourceType:     rawmsg[11:13],
						SourceTypeName: deviceTypeMap[rawmsg[11:13]],
						CommandCode:    rawmsg[41:45],
						CommandName:    commandsMap[rawmsg[41:45]],
					}
					log.Info().Int("len", length).Interface("msg", message).Msg(rawmsg)
				} else {
					// log.Debug().Int("len", length).Msg(rawmsg)
				}
			} else if rawmsg != "\u0011" {
				// log.Debug().Int("len", length).Msg(rawmsg)
			}
		}
	}
}
