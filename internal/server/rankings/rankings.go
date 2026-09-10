package rankings

import (
	"fmt"
	"net/http"
)

func RegisterRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleRoot)
	return mux

}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Response")
}
