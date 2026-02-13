package xslog

import (
	"context"
	"log/slog"
)

type structLoggerGrouped struct {
	handler    slog.Handler
	groupName  string
	groupAttrs []slog.Attr
	level      slog.Level
	levelValid bool
}

func (s *structLoggerGrouped) Enabled(ctx context.Context, level slog.Level) bool {
	if s.levelValid {
		return (level >= s.level)
	}

	return s.handler.Enabled(ctx, level)
}

func (s *structLoggerGrouped) Handle(ctx context.Context, r slog.Record) error {

	var gAttrs []slog.Attr
	if n := r.NumAttrs(); n > 0 {

		gAttrs = make([]slog.Attr, len(s.groupAttrs)+n)
		dst := copy(gAttrs, s.groupAttrs)
		r.Attrs(func(v slog.Attr) bool {
			gAttrs[dst] = v
			dst++
			return true
		})

		// Build a new record from the provided record so the group attributes
		// are included in the record passed to the parent handler.
		r = slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	} else {
		// cloning to clip the internal attrs slice and prevent accidental side effects on the original record since we will be modifying the attrs slice by adding group attributes to it.
		r = r.Clone()

		gAttrs = s.groupAttrs
	}

	r.AddAttrs(slog.GroupAttrs(s.groupName, gAttrs...))

	return s.handler.Handle(ctx, r)
}

func (s *structLoggerGrouped) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return s
	}

	// Note that we're clipping s.groupAttrs before using it in the append.
	//
	// This is intentionally not using slices.Clip to keep the hot path hot.
	attrs = append(s.groupAttrs[:len(s.groupAttrs):len(s.groupAttrs)], attrs...)

	return &structLoggerGrouped{s.handler, s.groupName, attrs, s.level, s.levelValid}
}

func (s *structLoggerGrouped) WithGroup(name string) slog.Handler {
	if name == "" {
		return s
	}

	return &structLoggerGrouped{s.handler, name, nil, s.level, s.levelValid}
}
