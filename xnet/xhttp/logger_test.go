package xhttp

import (
	"github.com/ballastworks/xs/xlog/xslog"
)

// ensures the request logger factory NewRequestLoggerFactory returns can be
// used wherever an xslog.LoggerFactory is expected.
var _ xslog.LoggerFactory = &xhttpLoggerFactory{}
