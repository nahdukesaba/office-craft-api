package models

type LoginSource string

const (
	LoginSourceDirect LoginSource = "direct"
	LoginSourceSSO    LoginSource = "sso"
)
