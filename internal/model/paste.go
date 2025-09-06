package model

import "time"

type Version struct {
	ZCode  []byte
	Lang   string
	Author string
	At     time.Time
}

type Paste struct {
	ID        string
	Lang      string
	Code      string
	Theme     string
	ExpiresAt time.Time

	Editable bool
	EditKey  string
	Author   string

	Versions  []Version
	CreatedAt time.Time
	UpdatedAt time.Time
}

