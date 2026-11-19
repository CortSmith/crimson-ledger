package domain

import "time"

const (
	// MaxContacts bounds one ping to 200 contacts (about 60 KB of JSON).
	MaxContacts = 200
	// MaxFixesPerContact bounds the position history one contact carries in one ping.
	MaxFixesPerContact = 50
	// MaxCharts bounds the map rectangles in one ping.
	MaxCharts = 50
	// MaxKills bounds the kill events in one ping.
	MaxKills = 200
	// MaxNameRunes bounds a contact name (WoW names are at most 12 letters; 24 leaves room for realms).
	MaxNameRunes = 24
	// MaxRawRunes bounds the raw log token a name was parsed from.
	MaxRawRunes = 64
	// MaxSourceLen bounds the logger's self-chosen source label.
	MaxSourceLen = 32
	// MaxZoneRunes bounds a chart's zone name.
	MaxZoneRunes = 64
	// MaxCoord is the upper bound of a map coordinate (percent of the map rectangle).
	MaxCoord = 100.0
	// MaxFixAge is how old a fix, contact or kill timestamp may be.
	MaxFixAge = 24 * time.Hour
	// MaxFixFuture is how far ahead of the server clock a timestamp may be.
	MaxFixFuture = 5 * time.Minute
	// MaxClockSkew is how far a ping's sentAt may differ from the server clock.
	MaxClockSkew = 10 * time.Minute
)
