package watcher

import (
	"bytes"
	"encoding/json"
)

type notunmarshaler *Event

// UnmarshalJSON Default implementation of JSON reads every integer as float.
func (event *Event) UnmarshalJSON(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewBuffer(contents))

	// we must enable UseNumber or decoder will decode any integer to float64
	decoder.UseNumber()

	// we must do such call or UnmarshalJSON will be called in recursion
	payload := notunmarshaler(event)

	err := decoder.Decode(&payload)
	if err != nil {
		return err
	}

	if number, ok := event.Value.(json.Number); ok {
		// if it looks like integer and smells like integer than it is
		// integer otherwise it is float
		if integer, err := number.Int64(); err == nil {
			event.Value = int(integer)

			if event.Kind == "status" {
				event.Value = Status(integer)
			}
		} else {
			floating, err := number.Float64()
			if err != nil {
				return err
			}

			event.Value = floating
		}
	}

	return nil
}
