package main

import (
	"bufio"
	"context"
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

	"cloud.google.com/go/bigquery"
	"github.com/alecthomas/kingpin"
	"github.com/ericchiang/k8s"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
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
	stateFilePath          = kingpin.Flag("state-file-path", "Path to file with state.").Default("/state/state.json").OverrideDefaultFromEnvar("STATE_FILE_PATH").String()
	stateFileConfigMapName = kingpin.Flag("state-file-configmap-name", "Name of the configmap with state file.").Default("evohome-hgi80-listener-state").OverrideDefaultFromEnvar("STATE_FILE_CONFIG_MAP_NAME").String()
	hgiDevicePath          = kingpin.Flag("hgi-device-path", "Path to usb device connecting HGI80.").Default("/dev/ttyUSB0").OverrideDefaultFromEnvar("HGI_DEVICE_PATH").String()
	evohomeID              = kingpin.Flag("evohome-id", "ID of the Evohome Touch device").Envar("EVOHOME_ID").Required().String()
	namespace              = kingpin.Flag("namespace", "Namespace the pod runs in.").Envar("NAMESPACE").Required().String()

	bigqueryProjectID = kingpin.Flag("bigquery-project-id", "Google Cloud project id that contains the BigQuery dataset").Envar("BQ_PROJECT_ID").Required().String()
	bigqueryDataset   = kingpin.Flag("bigquery-dataset", "Name of the BigQuery dataset").Envar("BQ_DATASET").Required().String()
	bigqueryTable     = kingpin.Flag("bigquery-table", "Name of the BigQuery table").Envar("BQ_TABLE").Required().String()

	zoneInfoMap map[int64]ZoneInfo

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

	// create kubernetes api client
	kubeClient, err := k8s.NewInClusterClient()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed creating Kubernetes API client")
	}

	// create command buffer and message processor
	commandQueue := make(chan Command, 100)
	messageProcessor := NewMessageProcessor(bigqueryClient, commandQueue)

	initBigqueryTable(bigqueryClient)

	readStateFromStateFile()

	log.Info().Msgf("Listening to serial usb device at %v for messages from evohome touch device with id %v...", *hgiDevicePath, *evohomeID)

	f, in := openSerialPort()
	defer closeSerialPort(f)

	// request zone names from controller approx once every 15 minutes to be able to store measurements with zone name and pick up changes / new zones
	go func() {
		for {
			for i := 0; i < 12; i++ {
				log.Info().Msgf("Queueing zone_name command for zone %v", i)
				commandQueue <- Command{
					messageType:   "RQ",
					commandName:   "zone_name",
					destinationID: *evohomeID,
					payload: &DefaultPayload{
						Values: []int{i, 0},
					},
				}
			}

			for i := 0; i < 12; i++ {
				log.Info().Msgf("Queueing zone_info command for zone %v", i)
				commandQueue <- Command{
					messageType:   "RQ",
					commandName:   "zone_info",
					destinationID: *evohomeID,
					payload: &DefaultPayload{
						Values: []int{i},
					},
				}
			}

			time.Sleep(time.Duration(applyJitter(900)) * time.Second)
		}
	}()

	go func() {
		time.Sleep(time.Duration(applyJitterWithPercentage(150, 5)) * time.Second)
		for {
			storeZoneInfoInBiqquery(bigqueryClient)
			time.Sleep(time.Duration(applyJitterWithPercentage(300, 5)) * time.Second)
		}
	}()

	go func() {
		time.Sleep(time.Duration(applyJitterWithPercentage(150, 5)) * time.Second)
		for {
			writeStateToConfigmap(kubeClient)
			time.Sleep(time.Duration(applyJitterWithPercentage(60, 5)) * time.Second)
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

	// test various commands to see their response
	log.Info().Msg("Queueing heartbeat / sysinfo command")
	commandQueue <- Command{
		messageType:   "RQ",
		commandName:   "heartbeat", // sysinfo
		destinationID: *evohomeID,
		payload: &DefaultPayload{
			Values: []int{0},
		},
	}

	log.Info().Msg("Queueing controller_mode command")
	commandQueue <- Command{
		messageType:   "RQ",
		commandName:   "controller_mode",
		destinationID: *evohomeID,
		payload: &DefaultPayload{
			Values: []int{255},
		},
	}

	log.Info().Msg("Queueing device_info command for device 0")
	commandQueue <- Command{
		messageType:   "RQ",
		commandName:   "device_info",
		destinationID: *evohomeID,
		payload: &DefaultPayload{
			Values: []int{0, 0, 0},
		},
	}

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

				// wait for serial port reset to finish before continuing
				waitGroup.Wait()

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

	zoneInfoMap = map[int64]ZoneInfo{
		252: ZoneInfo{
			ID:   252,
			Name: "Opentherm",
		},
	}

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

		zoneInfoMap = state.ZoneInfoMap
	}
}

func writeStateToConfigmap(kubeClient *k8s.Client) {

	// retrieve configmap
	var configMap corev1.ConfigMap
	err := kubeClient.Get(context.Background(), *namespace, *stateFileConfigMapName, &configMap)
	if err != nil {
		log.Error().Err(err).Msgf("Failed retrieving configmap %v", *stateFileConfigMapName)
	}

	// marshal state to json
	state := State{
		ZoneInfoMap: zoneInfoMap,
	}
	stateData, err := json.Marshal(state)

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	configMap.Data["state.json"] = string(stateData)

	// update configmap to have state available when the application runs the next time and for other applications
	err = kubeClient.Update(context.Background(), &configMap)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed updating configmap %v", *stateFileConfigMapName)
	}

	log.Info().Msgf("Stored state in configmap %v...", *stateFileConfigMapName)
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

	accumulatedTableName := *bigqueryTable + "_accumulated"

	log.Debug().Msgf("Checking if table %v.%v.%v exists...", *bigqueryProjectID, *bigqueryDataset, accumulatedTableName)
	tableExist = bigqueryClient.CheckIfTableExists(*bigqueryDataset, accumulatedTableName)
	if !tableExist {
		log.Debug().Msgf("Creating table %v.%v.%v...", *bigqueryProjectID, *bigqueryDataset, accumulatedTableName)
		err := bigqueryClient.CreateTable(*bigqueryDataset, accumulatedTableName, BigQueryHGIMeasurement{}, "inserted_at", true)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed creating bigquery table")
		}
	} else {
		log.Debug().Msgf("Trying to update table %v.%v.%v schema...", *bigqueryProjectID, *bigqueryDataset, accumulatedTableName)
		err := bigqueryClient.UpdateTableSchema(*bigqueryDataset, accumulatedTableName, BigQueryHGIMeasurement{})
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

func storeZoneInfoInBiqquery(bigqueryClient BigQueryClient) {
	measurement := BigQueryHGIMeasurement{
		InsertedAt: time.Now().UTC(),
	}

	for _, v := range zoneInfoMap {
		if v.IsActualZone() && v.Temperature != 0 && v.HeatDemand != 0 {
			measurement.Zones = append(measurement.Zones, BigQueryZone{
				ZoneID:      v.ID,
				ZoneName:    v.Name,
				Temperature: bigquery.NullFloat64{Float64: v.Temperature, Valid: true},
				Setpoint:    bigquery.NullFloat64{Float64: v.Setpoint, Valid: v.Setpoint > v.MinTemperature && v.Setpoint < v.MaxTemperature},
				HeatDemand:  bigquery.NullFloat64{Float64: v.HeatDemand, Valid: true},
			})
		}
	}

	err := bigqueryClient.InsertHGIMeasurements(*bigqueryDataset, *bigqueryTable+"_accumulated", []BigQueryHGIMeasurement{measurement})
	if err != nil {
		log.Error().Err(err).Msg("Failed inserting accumulated measurements into bigquery table")
	}
}
