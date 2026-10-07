package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/awhadi/blasta-perftest/internal/importer"
)

// handleImport reads a curl command, a HAR file, a Postman collection or an OpenAPI document and
// returns the requests in it. It only reads the text it is given: nothing is sent or stored, so it
// is open to visitors like the template catalogue.
func (a *API) handleImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, importer.MaxInput+4096))
	if err != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	res, err := importer.Parse(in.Text)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
