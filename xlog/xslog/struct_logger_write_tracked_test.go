package xslog

// ensures that the simpleHandler interface is always a superset of the Logger
// interface since simplerHandler is used as a method argument type that
// unifies the signature of xslog.Logger and slog.Handler to a common reduced
// signature.
var _ simpleHandler = ((Logger)(nil))
