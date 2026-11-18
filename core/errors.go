package core

// ErrorKind is the closed set of API error classes. Each protocol gives each
// kind its own status and body.
type ErrorKind int

const (
	InvalidRequest ErrorKind = iota + 1
	Authentication
	Billing
	Permission
	NotFound
	RequestTooLarge
	RateLimit
	APIError
	Timeout
	Overloaded
)

// ErrorKinds lists every kind, in a fixed order.
var ErrorKinds = []ErrorKind{InvalidRequest, Authentication, Billing, Permission, NotFound, RequestTooLarge, RateLimit, APIError, Timeout, Overloaded}

var errorNames = map[ErrorKind]string{
	InvalidRequest: "invalid_request", Authentication: "authentication", Billing: "billing", Permission: "permission",
	NotFound: "not_found", RequestTooLarge: "request_too_large", RateLimit: "rate_limit", APIError: "api_error",
	Timeout: "timeout", Overloaded: "overloaded",
}

// Name is the kind's name in scripts, such as "overloaded".
func (k ErrorKind) Name() string {
	n, ok := errorNames[k]
	if !ok {
		panic("core: unknown ErrorKind")
	}
	return n
}

// ParseErrorKind turns a script name into a kind.
func ParseErrorKind(name string) (ErrorKind, bool) {
	for _, k := range ErrorKinds {
		if k.Name() == name {
			return k, true
		}
	}
	return 0, false
}

// ErrorKindNames lists the script names of every kind, in order.
func ErrorKindNames() []string {
	out := make([]string, len(ErrorKinds))
	for i, k := range ErrorKinds {
		out[i] = k.Name()
	}
	return out
}

// Retry is the retry hint an error carries, where the protocol has one.
type Retry int

const (
	RetryDefault Retry = iota // no hint: the client decides by status
	RetryNever
	RetryAlways
)

// Window is the closed set of usage-limit windows.
type Window int

const (
	FiveHour Window = iota + 1
	SevenDay
	SevenDayOpus
	SevenDaySonnet
	// Daily is a per-day request quota (Gemini's requests-per-day).
	Daily
	// PerMinute is a per-minute quota (a plain rate limit that says when to retry).
	PerMinute
)

// Windows lists every window, in a fixed order.
var Windows = []Window{FiveHour, SevenDay, SevenDayOpus, SevenDaySonnet, Daily, PerMinute}

var windowNames = map[Window]string{
	FiveHour: "five_hour", SevenDay: "seven_day", SevenDayOpus: "seven_day_opus", SevenDaySonnet: "seven_day_sonnet",
	Daily: "daily", PerMinute: "per_minute",
}

// Name is the window's name in scripts.
func (w Window) Name() string {
	n, ok := windowNames[w]
	if !ok {
		panic("core: unknown Window")
	}
	return n
}

// ParseWindow turns a script name into a window.
func ParseWindow(name string) (Window, bool) {
	for _, w := range Windows {
		if w.Name() == name {
			return w, true
		}
	}
	return 0, false
}

// WindowNames lists the script names of every window, in order.
func WindowNames() []string {
	out := make([]string, len(Windows))
	for i, w := range Windows {
		out[i] = w.Name()
	}
	return out
}
