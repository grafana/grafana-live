package telegraf

import (
	"fmt"
	"time"

	"github.com/grafana/grafana-live-sdk/internal/frameutil"
	"github.com/grafana/grafana-live-sdk/telemetry"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/converters"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/parsers"
	"github.com/influxdata/telegraf/plugins/parsers/influx"
)

var _ telemetry.Converter = (*Converter)(nil)

// Converter converts Telegraf metrics to Grafana frames.
type Converter struct {
	parser parsers.Parser
}

// NewConverter creates new Converter from Influx/Telegraf format to Grafana Data Frames.
// This converter generates one frame for each input metric name and time combination.
func NewConverter() *Converter {
	return &Converter{
		parser: influx.NewParser(influx.NewMetricHandler()),
	}
}

// Each unique metric frame identified by name and time.
func getFrameKey(m telegraf.Metric) string {
	return m.Name() + "_" + m.Time().String()
}

// Convert metrics.
func (c *Converter) Convert(body []byte) ([]telemetry.FrameWrapper, error) {
	metrics, err := c.parser.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("error parsing metrics: %w", err)
	}

	// maintain the order of frames as they appear in input.
	var frameKeyOrder []string
	metricFrames := make(map[string]*metricFrame)

	for _, m := range metrics {
		frameKey := getFrameKey(m)
		frame, ok := metricFrames[frameKey]
		if ok {
			// Existing frame.
			err := frame.extend(m)
			if err != nil {
				return nil, err
			}
		} else {
			frameKeyOrder = append(frameKeyOrder, frameKey)
			frame = newMetricFrame(m)
			err = frame.extend(m)
			if err != nil {
				return nil, err
			}
			metricFrames[frameKey] = frame
		}
	}

	frameWrappers := make([]telemetry.FrameWrapper, 0, len(metricFrames))
	for _, key := range frameKeyOrder {
		frameWrappers = append(frameWrappers, metricFrames[key])
	}

	return frameWrappers, nil
}

type metricFrame struct {
	key    string
	fields []*data.Field
}

// newMetricFrame will return a new frame with length 1.
func newMetricFrame(m telegraf.Metric) *metricFrame {
	s := &metricFrame{
		key:    m.Name(),
		fields: make([]*data.Field, 1),
	}
	s.fields[0] = data.NewField("time", nil, []time.Time{m.Time()})
	return s
}

// Key returns a key which describes Frame metrics.
func (s *metricFrame) Key() string {
	return s.key
}

// Frame transforms metricFrame to Grafana data.Frame.
func (s *metricFrame) Frame() *data.Frame {
	return data.NewFrame(s.key, s.fields...)
}

// extend existing metricFrame fields.
func (s *metricFrame) extend(m telegraf.Metric) error {
	for _, f := range m.FieldList() {
		ft := frameutil.FieldTypeFor(f.Value)
		if ft == data.FieldTypeUnknown {
			return fmt.Errorf("unknown type: %t", f.Value)
		}

		// Make all fields nullable.
		ft = ft.NullableType()

		field := data.NewFieldFromFieldType(ft, 1)
		field.Name = f.Key
		field.Labels = m.Tags()

		var convert func(v interface{}) (interface{}, error)

		switch ft {
		case data.FieldTypeNullableString:
			convert = converters.AnyToNullableString.Converter
		case data.FieldTypeNullableFloat64:
			convert = converters.JSONValueToNullableFloat64.Converter
		case data.FieldTypeNullableBool:
			convert = converters.BoolToNullableBool.Converter
		case data.FieldTypeNullableInt64:
			convert = converters.JSONValueToNullableInt64.Converter
		default:
			return fmt.Errorf("no converter %s=%v (%T) %s", f.Key, f.Value, f.Value, ft.ItemTypeString())
		}

		v, err := convert(f.Value)
		if err != nil {
			return fmt.Errorf("value convert error: %v", err)
		}
		field.Set(0, v)
		s.fields = append(s.fields, field)
	}
	return nil
}
