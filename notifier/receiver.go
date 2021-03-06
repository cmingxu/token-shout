package notifier

import (
	"bytes"
	"errors"
	"net/http"
	"time"

	"github.com/858chain/token-shout/utils"
)

const HTTP_USER_AGENT = "858Chain/Token-Shout-Agent"

var ErrShouldRetry = errors.New("should retry")

func ShouldRetry(err error) bool {
	return err.Error() == "should retry"
}

// Receiver receive event notfication
type Receiver struct {
	retryCount          uint     `json:"retryCount"`
	endpoint            string   `json:"endpoint"`
	newBalanceRemaining float64  `json:"newBalanceRemaining"`
	eventTypes          []string `json:"evnetTypes"`
	fromAddresses       []string `json:"from"`
	toAddresses         []string `json:"to"`

	client *http.Client `json:"-"`
}

func NewReceiver(cfg ReceiverConfig) *Receiver {
	return &Receiver{
		retryCount:          cfg.RetryCount,
		endpoint:            cfg.Endpoint,
		newBalanceRemaining: cfg.NewBalanceRemaining,
		eventTypes:          cfg.EventTypes,
		fromAddresses:       cfg.FromAddresses,
		toAddresses:         cfg.ToAddresses,
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:       10,
				IdleConnTimeout:    30 * time.Second,
				DisableCompression: true,
			},
		},
	}
}

// Check if event type in receivier's eventTypes
func (r *Receiver) Match(event Event) bool {
	for _, et := range r.eventTypes {
		if et == event.Type() &&
			r.newBalanceMatch(event) &&
			r.fromAddrMatch(event) &&
			r.toAddrMatch(event) {
			return true
		}
	}

	return false
}

func (r *Receiver) newBalanceMatch(event Event) bool {
	newBalance, newBalanceFound := event.GetEvent()["newBalance"]
	if !newBalanceFound {
		return true
	}

	newBalanceCasted, newBalanceCastedOk := newBalance.(float64)
	if !newBalanceCastedOk {
		return true
	}

	return newBalanceCasted >= r.newBalanceRemaining
}

// TODO
func (r *Receiver) fromAddrMatch(event Event) bool {
	return true
}

// DODO
func (r *Receiver) toAddrMatch(event Event) bool {
	return true
}

// Accept event and spawn new goroutine to post event back to the endpoint.
func (r *Receiver) Accept(event Event) {
	utils.L.Infof("%s accept event %s", r.endpoint, event.Type())

	sendFunc := func(event Event) error {
		utils.L.Infof("sending event %s to %s", event.Type(), r.endpoint)
		eventBytes, err := EncodeEvent(event)
		if err != nil {
			utils.L.Error(err)
			return err
		}
		post, err := http.NewRequest(http.MethodPost, r.endpoint, bytes.NewBuffer(eventBytes))
		if err != nil {
			utils.L.Error(err)
			return err
		}

		post.Header.Set("User-Agent", HTTP_USER_AGENT)
		resp, err := r.client.Do(post)
		if err != nil {
			utils.L.Errorf("ErrShouldRetry : %v", err)
			return ErrShouldRetry
		}

		// should retry if endpoint does not return status code 200
		if resp.StatusCode != http.StatusOK {
			utils.L.Errorf("ErrShouldRetry statusCode: %v", resp.StatusCode)
			return ErrShouldRetry
		}

		return nil
	}

	go func(event Event) {
		err := sendFunc(event)
		if err == nil {
			return
		}

		retryRemains := r.retryCount
		backoffInterval := time.NewTicker(10 * time.Second)
		for {
			select {
			case <-backoffInterval.C:
				err := sendFunc(event)
				if err == nil {
					utils.L.Errorf("sendFunc err: %v", err)
					return
				} else {
					// stop retrying if serious error happend
					if !ShouldRetry(err) {
						utils.L.Errorf("retry err: %v", err)
						return
					}

					if retryRemains <= 0 {
						utils.L.Infof("stop posting event after n retries")
						return
					}

					retryRemains = retryRemains - 1
				}
			}
		}
	}(event)
}
