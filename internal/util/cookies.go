package util

import (
	"net/http"
	"time"
)

func WriteCookie(w http.ResponseWriter, name, value string, life time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(life),
		MaxAge:   int(life / time.Second),
		SameSite: http.SameSiteLaxMode,
	})
}

