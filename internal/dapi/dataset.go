package dapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

type DatasetField struct {
	Name string           `json:"name,omitempty"`
	Type DatasetFieldType `json:"type,omitempty"`
}

type DatasetFieldType struct {
	Name      string         `json:"name,omitempty"`
	SubSchema []DatasetField `json:"subSchema,omitempty"`
	Precision int            `json:"precision,omitempty"`
	Scale     int            `json:"scale,omitempty"`
}

type Dataset struct {
	CatalogEntity
	Type   string         `json:"type,omitempty"`
	Fields []DatasetField `json:"fields,omitempty"`
}

type VirtualDataset struct {
	Dataset
	Sql        string   `json:"sql,omitempty"`
	SqlContext []string `json:"sqlContext,omitempty"`
}

type PhysicalDataset struct {
	Dataset
	Format                    *PhysicalDatasetFormat            `json:"format,omitempty"`
	AccelerationRefreshPolicy *DatasetAccelerationRefreshPolicy `json:"accelerationRefreshPolicy,omitempty"`
}

// PhysicalDatasetFormat's bool fields mostly have no omitempty, matching
// the Source.AccelerationNeverExpire fix (see source.go): omitempty on a
// bool drops it whenever it's false, and Dremio falls back to its own
// default instead of the caller's explicit false.
//
// HasMergedCells is the one exception, and it's deliberate: it's an
// Excel-only field, and Dremio validates format fields against the format's
// `type` - sending it (even as false) for a non-Excel type (e.g. "Text"/CSV)
// gets rejected with `400 "Invalid value found at: format.hasMergedCells"`,
// confirmed live while verifying dremio_promoted_dataset's Framework
// migration. This is a different bug class from the omitempty one: the
// original "fix by inspection" (assuming this field behaved like Source's
// acceleration flags) was wrong for this specific field, since unlike
// those, PhysicalDatasetFormat's fields are type-conditional, not
// universally valid. Reverted to omitempty here so it's only sent when a
// caller actually sets it (Excel format).
type PhysicalDatasetFormat struct {
	Type                    string `json:"type,omitempty"`
	FieldDelimiter          string `json:"fieldDelimiter,omitempty"`
	LineDelimiter           string `json:"lineDelimiter,omitempty"`
	Quote                   string `json:"quote,omitempty"`
	Comment                 string `json:"comment,omitempty"`
	Escape                  string `json:"escape,omitempty"`
	SkipFirstLine           bool   `json:"skipFirstLine"`
	ExtractHeader           bool   `json:"extractHeader"`
	TrimHeader              bool   `json:"trimHeader"`
	AutoGenerateColumnNames bool   `json:"autoGenerateColumnNames"`
	SheetName               string `json:"sheetName,omitempty"`
	HasMergedCells          bool   `json:"hasMergedCells,omitempty"`
}

type DatasetAccelerationRefreshPolicy struct {
	RefreshPeriodMs int    `json:"refreshPeriodMs,omitempty"`
	GracePeriodMs   int    `json:"gracePeriodMs,omitempty"`
	Method          string `json:"method,omitempty"`
	RefreshField    string `json:"refreshField,omitempty"`
	NeverExpire     bool   `json:"neverExpire"`
	NeverRefresh    bool   `json:"neverRefresh"`
}

func (c *Client) GetDataset(id string) (*Dataset, error) {
	result := new(Dataset)
	err := c.getCatalogItem(id, result)
	if err != nil {
		return nil, err
	}
	if result.EntityType != "dataset" {
		return nil, errors.New("Catalog entity is not a dataset")
	}
	result.EnrichFields()
	return result, nil
}

func (c *Client) GetVirtualDataset(id string) (*VirtualDataset, error) {
	result := new(VirtualDataset)
	err := c.getCatalogItem(id, result)
	if err != nil {
		return nil, err
	}
	if result.EntityType != "dataset" {
		return nil, errors.New("Catalog entity is not a dataset")
	}
	if result.Type != "VIRTUAL_DATASET" {
		return nil, errors.New("Dataset is not a VIRTUAL_DATASET")
	}
	result.EnrichFields()
	return result, nil
}

func (c *Client) GetPhysicalDataset(id string) (*PhysicalDataset, error) {
	result := new(PhysicalDataset)
	err := c.getCatalogItem(id, result)
	if err != nil {
		return nil, err
	}
	if result.EntityType != "dataset" {
		return nil, errors.New("Catalog entity is not a dataset")
	}
	if result.Type != "PHYSICAL_DATASET" {
		return nil, errors.New("Dataset is not a PHYSICAL_DATASET")
	}
	result.EnrichFields()
	return result, nil
}

type NewVirtualDatasetSpec struct {
	Path       []string
	Sql        string
	SqlContext []string
}

func (c *Client) NewVirtualDataset(spec *NewVirtualDatasetSpec) (*VirtualDataset, error) {
	dataset := VirtualDataset{
		Dataset: Dataset{
			CatalogEntity: CatalogEntity{
				EntityType: "dataset",
				Path:       spec.Path,
			},
			Type: "VIRTUAL_DATASET",
		},
		Sql:        spec.Sql,
		SqlContext: spec.SqlContext,
	}
	result := new(VirtualDataset)
	err := c.newCatalogItem(dataset, result)
	if err != nil {
		return nil, err
	}
	result.EnrichFields()
	return result, nil
}

type UpdateVirtualDatasetSpec struct {
	Sql        string
	SqlContext []string
}

func (c *Client) UpdateVirtualDataset(id string, spec *UpdateVirtualDatasetSpec) (*VirtualDataset, error) {
	original, err := c.GetVirtualDataset(id)
	if err != nil {
		return nil, err
	}
	dataset := VirtualDataset{
		Dataset:    original.Dataset,
		Sql:        spec.Sql,
		SqlContext: spec.SqlContext,
	}
	result := new(VirtualDataset)
	err = c.updateCatalogItem(id, dataset, result)
	if err != nil {
		return nil, err
	}
	result.EnrichFields()
	return result, nil
}

type NewPhysicalDatasetSpec struct {
	Path                      []string
	Format                    *PhysicalDatasetFormat
	AccelerationRefreshPolicy *DatasetAccelerationRefreshPolicy
}

func (c *Client) NewPhysicalDataset(fileId string, spec *NewPhysicalDatasetSpec) (*PhysicalDataset, error) {
	dataset := PhysicalDataset{
		Dataset: Dataset{
			CatalogEntity: CatalogEntity{
				EntityType: "dataset",
				Path:       spec.Path,
			},
			Type: "PHYSICAL_DATASET",
		},
		Format:                    spec.Format,
		AccelerationRefreshPolicy: spec.AccelerationRefreshPolicy,
	}
	body, err := json.Marshal(dataset)
	if err != nil {
		return nil, err
	}
	result := new(PhysicalDataset)
	path := fmt.Sprintf("/api/v3/catalog/%s", url.QueryEscape(fileId))
	err = c.request("POST", path, bytes.NewBuffer(body), result)
	if err != nil {
		return nil, err
	}
	result.EnrichFields()
	return result, nil
}

type UpdatePhysicalDatasetSpec struct {
	Format                    *PhysicalDatasetFormat
	AccelerationRefreshPolicy *DatasetAccelerationRefreshPolicy
}

func (c *Client) UpdatePhysicalDataset(id string, spec *UpdatePhysicalDatasetSpec) (*PhysicalDataset, error) {
	original, err := c.GetPhysicalDataset(id)
	if err != nil {
		return nil, err
	}
	// A nil spec.Format means "leave the format alone" (e.g. a caller only
	// managing the acceleration policy, not the format), not "clear it".
	// Confirmed live: sending a PUT with no format at all makes Dremio
	// reject it outright with 404 "Promoted dataset needs to have a format
	// set" - this used to unconditionally send spec.Format as-is, which
	// broke every update for a caller that only ever set
	// AccelerationRefreshPolicy (dremio_physical_dataset, in both this fork
	// and the original upstream, never set Format at all).
	format := spec.Format
	if format == nil {
		format = original.Format
	}
	dataset := PhysicalDataset{
		Dataset:                   original.Dataset,
		Format:                    format,
		AccelerationRefreshPolicy: spec.AccelerationRefreshPolicy,
	}
	result := new(PhysicalDataset)
	err = c.updateCatalogItem(id, dataset, result)
	if err != nil {
		return nil, err
	}
	result.EnrichFields()
	return result, nil
}
