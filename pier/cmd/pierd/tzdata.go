package main

// The Logs chart cuts days at the viewer's midnights, in the time zone their
// browser names. A minimal server or container may have no zone files, so
// pierd carries its own.
import _ "time/tzdata"
