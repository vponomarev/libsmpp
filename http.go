package libsmpp

import "encoding/json"

func (s *SMPPSubmit) toJSON() (data []byte, err error) {
	data, err = json.Marshal(s)
	return
}

func (s *SMPPSubmit) fromJSON(data []byte) (err error) {
	err = json.Unmarshal(data, s)
	return
}
