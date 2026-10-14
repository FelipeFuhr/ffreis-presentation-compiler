package serve

// broker is unexported, so this file lives in package serve rather than
// serve_test.

import "testing"

func TestBrokerPublishDeliversToASubscriber(t *testing.T) {
	b := newBroker()
	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.publish()

	select {
	case <-ch:
	default:
		t.Fatal("subscriber did not receive the published signal")
	}
}

func TestBrokerPublishDoesNotBlockOnAFullSubscriber(t *testing.T) {
	b := newBroker()
	ch := b.subscribe()
	defer b.unsubscribe(ch)

	// publish twice before the subscriber reads: a reload signal already
	// pending is enough, so this must not deadlock on the buffered channel.
	b.publish()
	b.publish()

	<-ch
}

func TestBrokerPublishWithNoSubscribersIsANoop(t *testing.T) {
	b := newBroker()
	b.publish() // must not panic with an empty subscriber set
}

func TestUnsubscribeStopsFurtherDelivery(t *testing.T) {
	b := newBroker()
	ch := b.subscribe()
	b.unsubscribe(ch)

	b.publish()
	// ch is no longer tracked by the broker; nothing should arrive on it.
	select {
	case <-ch:
		t.Fatal("unsubscribed channel still received a signal")
	default:
	}
}
