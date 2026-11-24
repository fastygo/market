package ordering

import (
	"errors"
	"fmt"
)

// OrderStatus values (architecture "State machines").
type OrderStatus string

// Order statuses.
const (
	OrderPending         OrderStatus = "pending"
	OrderAwaitingPayment OrderStatus = "awaiting_payment"
	OrderPaid            OrderStatus = "paid"
	OrderFulfilled       OrderStatus = "fulfilled"
	OrderCancelled       OrderStatus = "cancelled"
	OrderRefundPending   OrderStatus = "refund_pending"
	OrderRefunded        OrderStatus = "refunded"
	OrderDisputed        OrderStatus = "disputed"
)

// PaymentStatus values, normalized from every provider.
type PaymentStatus string

// Payment statuses.
const (
	PaymentCreated   PaymentStatus = "created"
	PaymentPending   PaymentStatus = "pending"
	PaymentSucceeded PaymentStatus = "succeeded"
	PaymentCanceled  PaymentStatus = "canceled"
	PaymentFailed    PaymentStatus = "failed"
	PaymentRefunded  PaymentStatus = "refunded"
	PaymentDisputed  PaymentStatus = "disputed"
)

// ErrTransition is an illegal transition.
var ErrTransition = errors.New("ordering: illegal transition")

// PaymentTransition moves a payment to the status reported by the provider
// (FetchPayment is the source of truth). Same status is a no-op (ok, false).
func PaymentTransition(from, to PaymentStatus) (changed bool, err error) {
	if from == to {
		return false, nil
	}
	ok := false
	switch from {
	case PaymentCreated:
		ok = to == PaymentPending || to == PaymentSucceeded || to == PaymentCanceled || to == PaymentFailed
	case PaymentPending:
		ok = to == PaymentSucceeded || to == PaymentCanceled || to == PaymentFailed
	case PaymentCanceled, PaymentFailed:
		ok = to == PaymentSucceeded // late success reported by the provider
	case PaymentSucceeded:
		ok = to == PaymentRefunded || to == PaymentDisputed
	case PaymentDisputed:
		ok = to == PaymentSucceeded || to == PaymentRefunded
	}
	if !ok {
		return false, fmt.Errorf("%w: payment %s -> %s", ErrTransition, from, to)
	}
	return true, nil
}

// OrderOnPayment is the order status after its payment reached p.
// changed is false when the order stays as it is.
func OrderOnPayment(from OrderStatus, p PaymentStatus) (to OrderStatus, changed bool, err error) {
	switch p {
	case PaymentPending:
		if from == OrderPending {
			return OrderAwaitingPayment, true, nil
		}
		return from, false, nil
	case PaymentSucceeded:
		switch from {
		case OrderPending, OrderAwaitingPayment, OrderCancelled:
			return OrderPaid, true, nil // cancelled + late success -> paid
		case OrderPaid, OrderFulfilled, OrderRefundPending, OrderRefunded:
			return from, false, nil
		case OrderDisputed:
			return OrderPaid, true, nil
		}
	case PaymentCanceled, PaymentFailed:
		switch from {
		case OrderPending, OrderAwaitingPayment:
			return OrderCancelled, true, nil
		default:
			return from, false, nil
		}
	case PaymentCreated:
		return from, false, nil
	}
	return from, false, fmt.Errorf("%w: order %s on payment %s", ErrTransition, from, p)
}
