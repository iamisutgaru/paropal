package main

import (
	_ "embed"
	"net/http"
	"strconv"
)

//go:generate ./scripts/build-sjb-tar.sh
//go:embed static/sjb.tar.gz
var sjbTarGz []byte

func (a *app) handleSjbTar(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", strconv.Itoa(len(sjbTarGz)))
	_, _ = w.Write(sjbTarGz)
}
