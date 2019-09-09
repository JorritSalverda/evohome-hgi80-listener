package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"os"
	"runtime"
	"strings"
	"sync"
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

	bigqueryProjectID = kingpin.Flag("bigquery-project-id", "Google Cloud project id that contains the BigQuery dataset").Envar("BQ_PROJECT_ID").Required().String()
	bigqueryDataset   = kingpin.Flag("bigquery-dataset", "Name of the BigQuery dataset").Envar("BQ_DATASET").Required().String()
	bigqueryTable     = kingpin.Flag("bigquery-table", "Name of the BigQuery table").Envar("BQ_TABLE").Required().String()

	zoneNames map[int64]string = map[int64]string{
		252: "Opentherm",
	}

	lastReceivedMessage = time.Now().UTC()
)

func main() {

	// parse command line parameters
	kingpin.Parse()

	initLogging()

	// log startup message
	log.Info().
		Str("branch", branch).
		Str("revision", revision).
		Str("buildDate", buildDate).
		Str("goVersion", goVersion).
		Msgf("Starting %v version %v...", app, version)

	bigqueryClient, err := NewBigQueryClient(*bigqueryProjectID)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed creating bigquery client")
	}

	initBigqueryTable(bigqueryClient)

	// create command buffer
	commandQueue := make(chan Command, 100)

	messageProcessor := NewMessageProcessor(bigqueryClient, commandQueue)

	log.Info().Msgf("Listening to serial usb device at %v for messages from evohome touch device with id %v...", *hgiDevicePath, *evohomeID)

	f, in := openSerialPort()
	defer closeSerialPort(f)

	// request zone names from controller approx once an hour to be able to store measurements with zone name and pick up changes / new zones
	go func() {
		for {
			for i := 0; i < 12; i++ {
				log.Info().Msgf("Queueing zone_name command for zone %v", i)
				commandQueue <- Command{
					messageType:   "RQ",
					commandName:   "zone_name",
					destinationID: *evohomeID,
					payload: &ZoneNamePayload{
						zoneID: i,
					},
				}
			}

			time.Sleep(time.Duration(applyJitter(3600)) * time.Second)
		}
	}()

	waitGroup := &sync.WaitGroup{}

	go func(waitGroup *sync.WaitGroup) {
		for {
			time.Sleep(time.Duration(applyJitter(120)) * time.Second)

			if time.Since(lastReceivedMessage).Minutes() > 2 {
				// reset serial port

				log.Info().Msg("Received last message more than 2 minutes ago, resetting serial port...")

				waitGroup.Add(1)
				closeSerialPort(f)
				f, in = openSerialPort()
				defer closeSerialPort(f)
				waitGroup.Done()
			}
		}
	}(waitGroup)

	for {
		// wait for serial port reset to finish before continuing
		waitGroup.Wait()

		// check if there's any commands to send
		select {
		case command := <-commandQueue:
			messageProcessor.SendCommand(f, command)
		default:
		}

		buf, isPrefix, err := in.ReadLine()

		if err != nil {
			if err != io.EOF {
				log.Warn().Err(err).Msg("Error reading from serial port, resetting port...")

				closeSerialPort(f)
				f, in = openSerialPort()
				defer closeSerialPort(f)
			}
		} else if isPrefix {
			log.Warn().Str("_msg", string(buf)).Msgf("Message is too long for buffer and split over multiple lines")
		} else {
			rawmsg := string(buf)
			length := len(rawmsg)

			// make sure no obvious errors in getting the data....
			if length > 40 &&
				!strings.Contains(rawmsg, "_ENC") &&
				!strings.Contains(rawmsg, "_BAD") &&
				!strings.Contains(rawmsg, "BAD") &&
				!strings.Contains(rawmsg, "ERR") {

				lastReceivedMessage = time.Now().UTC()

				isValidMessage, err := messageProcessor.IsValidMessage(rawmsg)
				if err != nil {
					log.Warn().Err(err).
						Str("_msg", rawmsg).
						Msg("Message is not valid")
				}

				if isValidMessage {
					message := messageProcessor.DecodeMessage(rawmsg)
					messageProcessor.ProcessMessage(message)
				}
			}
		}
	}
}

func initLogging() {
	// log as severity for stackdriver logging to recognize the level
	zerolog.LevelFieldName = "severity"

	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	output.FormatLevel = func(i interface{}) string {
		return ""
	}
	output.FormatMessage = func(i interface{}) string {
		return fmt.Sprintf("%s", i)
	}
	output.FormatFieldName = func(i interface{}) string {
		return fmt.Sprintf("| %s: ", i)
	}
	output.FormatFieldValue = func(i interface{}) string {
		return fmt.Sprintf("%s", i)
	}

	log.Logger = zerolog.New(output).With().Timestamp().Logger()

	// use zerolog for any logs sent via standard log library
	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)

}

func readStateFromStateFile() {
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
}

func initBigqueryTable(bigqueryClient BigQueryClient) {

	log.Debug().Msgf("Checking if table %v.%v.%v exists...", *bigqueryProjectID, *bigqueryDataset, *bigqueryTable)
	tableExist := bigqueryClient.CheckIfTableExists(*bigqueryDataset, *bigqueryTable)
	if !tableExist {
		log.Debug().Msgf("Creating table %v.%v.%v...", *bigqueryProjectID, *bigqueryDataset, *bigqueryTable)
		err := bigqueryClient.CreateTable(*bigqueryDataset, *bigqueryTable, BigQueryMeasurement{}, "inserted_at", true)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed creating bigquery table")
		}
	} else {
		log.Debug().Msgf("Trying to update table %v.%v.%v schema...", *bigqueryProjectID, *bigqueryDataset, *bigqueryTable)
		err := bigqueryClient.UpdateTableSchema(*bigqueryDataset, *bigqueryTable, BigQueryMeasurement{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed updating bigquery table schema")
		}
	}
}

func openSerialPort() (io.ReadWriteCloser, *bufio.Reader) {
	options := serial.OpenOptions{
		PortName:               *hgiDevicePath,
		BaudRate:               115200,
		DataBits:               8,
		StopBits:               1,
		MinimumReadSize:        0,
		InterCharacterTimeout:  2000,
		ParityMode:             serial.PARITY_NONE,
		Rs485Enable:            false,
		Rs485RtsHighDuringSend: false,
		Rs485RtsHighAfterSend:  false,
	}

	f, err := serial.Open(options)
	if err != nil {
		log.Fatal().Err(err).Interface("options", options).Msg("Failed opening serial device")
	}

	return f, bufio.NewReader(f)
}

func closeSerialPort(f io.ReadWriteCloser) {
	f.Close()

	time.Sleep(5 * time.Second)
}
