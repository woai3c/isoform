package main

// toastOptions is what a notification adds to a plain desktop toast: one
// action button, the same action the phone push offers, and hooks that report
// what the user did so the engagement receipts match the phone's (shown is
// delivered, a click on the body is opened, the button is acted, the close
// control is dismissed). A toast that times out reports nothing. Hooks run on
// their own goroutine and may be nil.
type toastOptions struct {
	ActionLabel string
	OnShown     func()
	OnOpened    func()
	OnActed     func()
	OnDismissed func()
}
