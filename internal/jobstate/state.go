package jobstate

type Status string

const (
	Pending    Status = "pending"
	Accepted   Status = "accepted"
	Queued     Status = "queued"
	Processing Status = "processing"
	RetryWait  Status = "retry_wait"
	Completed  Status = "completed"
	Failed     Status = "failed"
	Dead       Status = "dead"
	Cancelled  Status = "cancelled"
	Expired    Status = "expired"
)

var transitions = map[Status]map[Status]bool{
	Pending: {
		Accepted:  true,
		Cancelled: true,
	},
	Accepted: {
		Queued:    true,
		Cancelled: true,
	},
	Queued: {
		Processing: true,
		Cancelled:  true,
		Expired:    true,
	},
	Processing: {
		Completed: true,
		RetryWait: true,
		Failed:    true,
		Cancelled: true,
	},
	RetryWait: {
		Processing: true,
		Dead:       true,
		Cancelled:  true,
		Expired:    true,
	},
	Dead: {
		Queued: true,
	},
	Completed: {},
	Failed:    {},
	Cancelled: {},
	Expired:   {},
}

func CanTransition(from, to Status) bool {
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}
