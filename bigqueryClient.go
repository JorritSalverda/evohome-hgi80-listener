package main

import (
	"context"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/rs/zerolog/log"
)

// BigQueryClient is the interface for connecting to bigquery
type BigQueryClient interface {
	CheckIfDatasetExists(dataset string) bool
	CheckIfTableExists(dataset, table string) bool
	CreateTable(dataset, table string, typeForSchema interface{}, partitionField string, waitReady bool) error
	UpdateTableSchema(dataset, table string, typeForSchema interface{}) error
	DeleteTable(dataset, table string) error
	InsertMeasurements(dataset, table string, measurements []BigQueryMeasurement) error
}

type bigQueryClientImpl struct {
	client *bigquery.Client
	enable bool
}

// NewBigQueryClient returns new BigQueryClient
func NewBigQueryClient(projectID string, enable bool) (BigQueryClient, error) {

	ctx := context.Background()

	bigqueryClient, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return nil, err
	}

	return &bigQueryClientImpl{
		client: bigqueryClient,
		enable: enable,
	}, nil
}

func (bqc *bigQueryClientImpl) CheckIfDatasetExists(dataset string) bool {

	if !bqc.enable {
		return false
	}

	ds := bqc.client.Dataset(dataset)

	md, err := ds.Metadata(context.Background())

	log.Error().Err(err).Msgf("Error retrieving metadata for dataset %v", dataset)

	return md != nil
}

func (bqc *bigQueryClientImpl) CheckIfTableExists(dataset, table string) bool {

	if !bqc.enable {
		return false
	}

	tbl := bqc.client.Dataset(dataset).Table(table)

	md, _ := tbl.Metadata(context.Background())

	// log.Error().Err(err).Msgf("Error retrieving metadata for table %v", table)

	return md != nil
}

func (bqc *bigQueryClientImpl) CreateTable(dataset, table string, typeForSchema interface{}, partitionField string, waitReady bool) error {

	if !bqc.enable {
		return nil
	}

	tbl := bqc.client.Dataset(dataset).Table(table)

	// infer the schema of the type
	schema, err := bigquery.InferSchema(typeForSchema)
	if err != nil {
		return err
	}

	tableMetadata := &bigquery.TableMetadata{
		Schema: schema,
	}

	// if partitionField is set use it for time partitioning
	if partitionField != "" {
		tableMetadata.TimePartitioning = &bigquery.TimePartitioning{
			Field: partitionField,
		}
	}

	// create the table
	err = tbl.Create(context.Background(), tableMetadata)
	if err != nil {
		return err
	}

	if waitReady {
		for {
			if bqc.CheckIfTableExists(dataset, table) {
				break
			}
			time.Sleep(time.Second)
		}
	}

	return nil
}

func (bqc *bigQueryClientImpl) UpdateTableSchema(dataset, table string, typeForSchema interface{}) error {

	if !bqc.enable {
		return nil
	}

	tbl := bqc.client.Dataset(dataset).Table(table)

	// infer the schema of the type
	schema, err := bigquery.InferSchema(typeForSchema)
	if err != nil {
		return err
	}

	meta, err := tbl.Metadata(context.Background())
	if err != nil {
		return err
	}

	update := bigquery.TableMetadataToUpdate{
		Schema: schema,
	}
	if _, err := tbl.Update(context.Background(), update, meta.ETag); err != nil {
		return err
	}

	return nil
}

func (bqc *bigQueryClientImpl) DeleteTable(dataset, table string) error {

	if !bqc.enable {
		return nil
	}

	tbl := bqc.client.Dataset(dataset).Table(table)

	// delete the table
	err := tbl.Delete(context.Background())

	if err != nil {
		return err
	}

	return nil
}

func (bqc *bigQueryClientImpl) InsertMeasurements(dataset, table string, measurements []BigQueryMeasurement) error {

	if !bqc.enable {
		return nil
	}

	tbl := bqc.client.Dataset(dataset).Table(table)

	u := tbl.Uploader()

	if err := u.Put(context.Background(), measurements); err != nil {
		return err
	}

	return nil
}
