//go:build !linux

package sysstat

import "errors"

// BLASTA runs in a Linux container, so only Linux is supported. Elsewhere the
// API answers 503 and the UI does not show the meters.
func platformRead() (raw, error) { return raw{}, errors.New("system statistics need Linux") }
