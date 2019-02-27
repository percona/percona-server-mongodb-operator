package backup

// Method is an interface describing a method of backup
// for the raw MongoDB database data
type Method interface {
	LastError() error
	Start() error
	Stop() error
	Wait() error
}
