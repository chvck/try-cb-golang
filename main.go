package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/couchbase/gocb/v2"
	cbsearch "github.com/couchbase/gocb/v2/search"
	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
)

var (
	cbConnStr    = "couchbase://localhost"
	cbDataBucket = "travel-sample"
	cbUserBucket = "travel-users"
	cbUsername   = "Administrator"
	cbPassword   = "password"
	jwtSecret    = []byte("UNSECURE_SECRET_TOKEN")
)

var (
	ErrUserExists    = errors.New("user already exists")
	ErrUserNotFound  = errors.New("user does not exist")
	ErrBadPassword   = errors.New("password does not match")
	ErrBadAuthHeader = errors.New("bad authentication header format")
	ErrBadAuth       = errors.New("invalid auth token")
)

var globalCluster *gocb.Cluster
var globalBucket *gocb.Bucket
var globalCollection *gocb.Collection
var userBucket *gocb.Bucket
var userCollection *gocb.Collection
var flightCollection *gocb.Collection

type jsonBookedFlight struct {
	Name               string  `json:"name"`
	Flight             string  `json:"flight"`
	Price              float64 `json:"price"`
	Date               string  `json:"date"`
	SourceAirport      string  `json:"sourceairport"`
	DestinationAirport string  `json:"destinationairport"`
	BookedOn           string  `json:"bookedon"`
}

type jsonUser struct {
	Name     string   `json:"name"`
	Password string   `json:"password"`
	Flights  []string `json:"flights"`
}

type jsonFlight struct {
	Name               string  `json:"name"`
	Flight             string  `json:"flight"`
	Equipment          string  `json:"equipment"`
	Utc                string  `json:"utc"`
	SourceAirport      string  `json:"sourceairport"`
	DestinationAirport string  `json:"destinationairport"`
	Price              float64 `json:"price"`
	FlightTime         int     `json:"flighttime"`
}

type jsonAirport struct {
	AirportName string `json:"airportname"`
}

type jsonHotel struct {
	Country     string `json:"country"`
	City        string `json:"city"`
	State       string `json:"state"`
	Address     string `json:"address"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type jsonContext []string

func (c *jsonContext) Add(msg string) {
	*c = append(*c, msg)
}

type jsonFailure struct {
	Failure string `json:"failure"`
}

func writeJsonFailure(w http.ResponseWriter, code int, err error) {
	failObj := jsonFailure{
		Failure: err.Error(),
	}

	failBytes, err := json.Marshal(failObj)
	if err != nil {
		panic(err)
	}

	w.WriteHeader(code)
	w.Write(failBytes)
}

func decodeReqOrFail(w http.ResponseWriter, req *http.Request, data interface{}) bool {
	err := json.NewDecoder(req.Body).Decode(data)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return false
	}
	return true
}

func encodeRespOrFail(w http.ResponseWriter, data interface{}) {
	err := json.NewEncoder(w).Encode(data)
	if err != nil {
		writeJsonFailure(w, 500, err)
	}
}

func createJwtToken(user string) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user": user,
	}).SignedString(jwtSecret)
}

type AuthedUser struct {
	Name string
}

func decodeAuthUserOrFail(w http.ResponseWriter, req *http.Request, user *AuthedUser) bool {
	authHeader := req.Header.Get("Authorization")
	authHeaderParts := strings.SplitN(authHeader, " ", 2)
	if authHeaderParts[0] != "Bearer" {
		authHeader = req.Header.Get("Authentication")
		authHeaderParts = strings.SplitN(authHeader, " ", 2)
		if authHeaderParts[0] != "Bearer" {
			writeJsonFailure(w, 400, ErrBadAuthHeader)
			return false
		}
	}

	authToken := authHeaderParts[1]
	token, err := jwt.Parse(authToken, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return jwtSecret, nil
	})
	if err != nil {
		writeJsonFailure(w, 400, ErrBadAuthHeader)
		return false
	}

	authUser := token.Claims.(jwt.MapClaims)["user"].(string)
	if authUser == "" {
		writeJsonFailure(w, 400, ErrBadAuth)
		return false
	}

	user.Name = authUser

	return true
}

// GET /api/airports?search=xxx
type jsonAirportSearchResp struct {
	Data    []jsonAirport `json:"data"`
	Context jsonContext   `json:"context"`
}

func AirportSearch(w http.ResponseWriter, req *http.Request) {
	var respData jsonAirportSearchResp

	searchKey := req.FormValue("search")

	var queryStr string
	if len(searchKey) == 3 {
		queryStr = fmt.Sprintf("SELECT airportname FROM `travel-sample` WHERE faa='%s'", strings.ToUpper(searchKey))
	} else if len(searchKey) == 4 && searchKey == strings.ToUpper(searchKey) {
		queryStr = fmt.Sprintf("SELECT airportname FROM `travel-sample` WHERE icao ='%s'", searchKey)
	} else {
		queryStr = fmt.Sprintf("SELECT airportname FROM `travel-sample` WHERE airportname like '%s%%'", searchKey)
	}

	respData.Context.Add(queryStr)
	rows, err := globalCluster.Query(queryStr, nil)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	respData.Data = []jsonAirport{}
	for rows.Next() {
		var airport jsonAirport
		err := rows.Row(&airport)
		if err != nil {
			writeJsonFailure(w, 500, err)
			return
		}

		respData.Data = append(respData.Data, airport)
		airport = jsonAirport{}
	}

	if err := rows.Err(); err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	encodeRespOrFail(w, respData)
}

// GET /api/flightPaths/{from}/{to}?leave=mm/dd/YYYY
type jsonFlightSearchResp struct {
	Data    []jsonFlight `json:"data"`
	Context jsonContext  `json:"context"`
}

func FlightSearch(w http.ResponseWriter, req *http.Request) {
	var respData jsonFlightSearchResp

	reqVars := mux.Vars(req)
	leaveDate, err := time.Parse("01/02/2006", req.FormValue("leave"))
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	fromAirport := reqVars["from"]
	toAirport := reqVars["to"]
	dayOfWeek := int(leaveDate.Weekday())

	var queryStr string
	queryStr =
		"SELECT faa FROM `travel-sample` WHERE airportname='" + fromAirport + "'" +
			" UNION" +
			" SELECT faa FROM `travel-sample` WHERE airportname='" + toAirport + "'"

	respData.Context.Add(queryStr)
	rows, err := globalCluster.Query(queryStr, nil)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	var fromAirportFaa string
	var toAirportFaa string

	var airportInfo struct {
		Faa string `json:"faa"`
	}
	rows.Next()
	err = rows.Row(&airportInfo)
	if err != nil {
		if errors.Is(err, gocb.ErrNoResult) {
			encodeRespOrFail(w, respData)
			return
		}

		writeJsonFailure(w, 500, err)
		return
	}

	fromAirportFaa = airportInfo.Faa

	rows.Next()
	err = rows.Row(&airportInfo)
	if err != nil {
		if errors.Is(err, gocb.ErrNoResult) {
			encodeRespOrFail(w, respData)
			return
		}

		writeJsonFailure(w, 500, err)
		return
	}

	toAirportFaa = airportInfo.Faa

	err = rows.Close()
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	queryStr =
		"SELECT a.name, s.flight, s.utc, r.sourceairport, r.destinationairport, r.equipment" +
			" FROM `travel-sample` AS r" +
			" UNNEST r.schedule AS s" +
			" JOIN `travel-sample` AS a ON KEYS r.airlineid" +
			" WHERE r.sourceairport = '" + toAirportFaa + "'" +
			" AND r.destinationairport = '" + fromAirportFaa + "'" +
			" AND s.day=" + strconv.Itoa(dayOfWeek) +
			" ORDER BY a.name ASC;"

	respData.Context.Add(queryStr)
	rows, err = globalCluster.Query(queryStr, nil)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	respData.Data = []jsonFlight{}
	for rows.Next() {
		var flight jsonFlight
		err := rows.Row(&flight)
		if err != nil {
			writeJsonFailure(w, 500, err)
			return
		}
		flight.FlightTime = int(math.Ceil(rand.Float64() * 8000))
		flight.Price = math.Ceil(float64(flight.FlightTime)/8*100) / 100
		respData.Data = append(respData.Data, flight)
		flight = jsonFlight{}
	}

	if err := rows.Err(); err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	encodeRespOrFail(w, respData)
}

// POST /api/user/login
type jsonUserLoginReq struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

type jsonUserLoginResp struct {
	Data struct {
		Token string `json:"token"`
	} `json:"data"`
	Context jsonContext `json:"context"`
}

func UserLogin(w http.ResponseWriter, req *http.Request) {
	var respData jsonUserLoginResp
	var reqData jsonUserLoginReq
	if !decodeReqOrFail(w, req, &reqData) {
		return
	}
	userKey := reqData.User
	passRes, err := userCollection.LookupIn(userKey, []gocb.LookupInSpec{
		gocb.GetSpec("password", nil),
	}, nil)
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		writeJsonFailure(w, 401, ErrUserNotFound)
		return
	} else if err != nil {
		fmt.Println(err.Error())
		writeJsonFailure(w, 500, err)
		return
	}

	var password string
	err = passRes.ContentAt(0, &password)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	if password != reqData.Password {
		writeJsonFailure(w, 401, ErrBadPassword)
		return
	}

	token, err := createJwtToken(reqData.User)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	respData.Data.Token = token

	encodeRespOrFail(w, respData)
}

// POST /api/user/signup
type jsonUserSignupReq struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

type jsonUserSignupResp struct {
	Data struct {
		Token string `json:"token"`
	} `json:"data"`
	Context jsonContext `json:"context"`
}

func UserSignup(w http.ResponseWriter, req *http.Request) {
	var respData jsonUserSignupResp
	var reqData jsonUserSignupReq
	if !decodeReqOrFail(w, req, &reqData) {
		return
	}

	userKey := reqData.User
	user := jsonUser{
		Name:     reqData.User,
		Password: reqData.Password,
		Flights:  nil,
	}
	_, err := userCollection.Insert(userKey, user, nil)
	if errors.Is(err, gocb.ErrDocumentExists) {
		writeJsonFailure(w, 409, ErrUserExists)
		return
	} else if err != nil {
		fmt.Println(reflect.TypeOf(err))
		writeJsonFailure(w, 500, err)
		return
	}

	token, err := createJwtToken(user.Name)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	respData.Data.Token = token

	encodeRespOrFail(w, respData)
}

// GET /api/user/{username}/flights
type jsonUserFlightsResp struct {
	Data    []jsonBookedFlight `json:"data"`
	Context jsonContext        `json:"context"`
}

func UserFlights(w http.ResponseWriter, req *http.Request) {
	var respData jsonUserFlightsResp
	var authUser AuthedUser

	if !decodeAuthUserOrFail(w, req, &authUser) {
		return
	}

	userKey := authUser.Name

	var flightIDs []string
	res, err := userCollection.LookupIn(userKey, []gocb.LookupInSpec{
		gocb.GetSpec("flights", nil),
	}, nil)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	err = res.ContentAt(0, &flightIDs)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	var flight jsonBookedFlight
	var flights []jsonBookedFlight
	for _, flightID := range flightIDs {
		res, err := flightCollection.Get(flightID, nil)
		if err != nil {
			writeJsonFailure(w, 500, err)
			return
		}
		err = res.Content(&flight)
		if err != nil {
			fmt.Printf("Failed to get content from flight: %s\n", err)
			continue
		}
		flights = append(flights, flight)
	}

	respData.Data = flights

	encodeRespOrFail(w, respData)
}

// POST  /api/user/{username}/flights
type jsonUserBookFlightReq struct {
	Flights []jsonBookedFlight `json:"flights"`
}

type jsonUserBookFlightResp struct {
	Data struct {
		Added []jsonBookedFlight `json:"added"`
	} `json:"data"`
	Context jsonContext `json:"context"`
}

func UserBookFlight(w http.ResponseWriter, req *http.Request) {
	var respData jsonUserBookFlightResp
	var reqData jsonUserBookFlightReq
	var authUser AuthedUser

	if !decodeAuthUserOrFail(w, req, &authUser) {
		return
	}

	if !decodeReqOrFail(w, req, &reqData) {
		return
	}

	userKey := authUser.Name
	var user jsonUser
	res, err := userCollection.Get(userKey, nil)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}
	cas := res.Cas()
	err = res.Content(&user)
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	for _, flight := range reqData.Flights {
		flight.BookedOn = time.Now().Format("01/02/2006")
		respData.Data.Added = append(respData.Data.Added, flight)
		flightID, err := uuid.NewRandom()
		if err != nil {
			writeJsonFailure(w, 500, err)
		}
		user.Flights = append(user.Flights, flightID.String())
		_, err = flightCollection.Upsert(flightID.String(), flight, nil)
		if err != nil {
			writeJsonFailure(w, 500, err)
		}
	}

	opts := gocb.ReplaceOptions{Cas: cas}
	_, err = userCollection.Replace(userKey, user, &opts)
	if err != nil {
		// We intentionally do not handle CAS mismatch, as if the users
		//  account was already modified, they probably want to know.
		writeJsonFailure(w, 500, err)
		return
	}

	encodeRespOrFail(w, respData)
}

// GET /api/hotel/{description}/{location}
type jsonHotelSearchResp struct {
	Data    []jsonHotel `json:"data"`
	Context jsonContext `json:"context"`
}

func HotelSearch(w http.ResponseWriter, req *http.Request) {
	var respData jsonHotelSearchResp

	reqVars := mux.Vars(req)
	description := reqVars["description"]
	location := reqVars["location"]

	qp := cbsearch.NewConjunctionQuery(cbsearch.NewTermQuery("hotel").Field("type"))

	if location != "" && location != "*" {
		qp.And(cbsearch.NewDisjunctionQuery(
			cbsearch.NewMatchPhraseQuery(location).Field("country"),
			cbsearch.NewMatchPhraseQuery(location).Field("city"),
			cbsearch.NewMatchPhraseQuery(location).Field("state"),
			cbsearch.NewMatchPhraseQuery(location).Field("address"),
		))
	}

	if description != "" && description != "*" {
		qp.And(cbsearch.NewDisjunctionQuery(
			cbsearch.NewMatchPhraseQuery(description).Field("description"),
			cbsearch.NewMatchPhraseQuery(description).Field("name"),
		))
	}

	results, err := globalCluster.SearchQuery("hotels", qp, &gocb.SearchOptions{Limit: 100})
	if err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	respData.Data = []jsonHotel{}
	for results.Next() {
		hit := results.Row()
		res, err := globalCollection.LookupIn(hit.ID, []gocb.LookupInSpec{
			gocb.GetSpec("country", nil),
			gocb.GetSpec("city", nil),
			gocb.GetSpec("state", nil),
			gocb.GetSpec("address", nil),
			gocb.GetSpec("name", nil),
			gocb.GetSpec("description", nil),
		}, nil)
		if err != nil {
			writeJsonFailure(w, 500, err)
			return
		}
		// We only log errors here because being unable to retrieve one of the hotel fields isn't fatal to
		// our request.

		var hotel jsonHotel
		if res.Exists(0) {
			err = res.ContentAt(0, &hotel.Country)
			if err != nil {
				fmt.Println(err)
			}
		}
		if res.Exists(1) {
			err = res.ContentAt(1, &hotel.City)
			if err != nil {
				fmt.Println(err)
			}
		}
		if res.Exists(2) {
			err = res.ContentAt(2, &hotel.State)
			if err != nil {
				fmt.Println(err)
			}
		}
		if res.Exists(3) {
			err = res.ContentAt(3, &hotel.Address)
			if err != nil {
				fmt.Println(err)
			}
		}
		if res.Exists(4) {
			err = res.ContentAt(4, &hotel.Name)
			if err != nil {
				fmt.Println(err)
			}
		}
		if res.Exists(5) {
			err = res.ContentAt(5, &hotel.Description)
			if err != nil {
				fmt.Println(err)
			}
		}
		respData.Data = append(respData.Data, hotel)
	}

	if err := results.Err(); err != nil {
		writeJsonFailure(w, 500, err)
		return
	}

	encodeRespOrFail(w, respData)
}

func main() {
	var err error

	// Connect to Couchbase
	clusterOpts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{
			Username: cbUsername,
			Password: cbPassword,
		},
	}
	globalCluster, err = gocb.Connect(cbConnStr, clusterOpts)
	if err != nil {
		panic(err)
	}

	// Open the bucket
	globalBucket = globalCluster.Bucket(cbDataBucket)
	userBucket = globalCluster.Bucket(cbUserBucket)

	// Select the required collections
	globalCollection = globalBucket.DefaultCollection()
	userDataScope := userBucket.Scope("userData")
	userCollection = userDataScope.Collection("users")
	flightCollection = userDataScope.Collection("flights")

	// Create a router for our server
	r := mux.NewRouter()

	// Set up our REST endpoints
	r.Path("/api/airports").Methods("GET").HandlerFunc(AirportSearch)
	r.Path("/api/flightPaths/{from}/{to}").Methods("GET").HandlerFunc(FlightSearch)
	r.Path("/api/user/login").Methods("POST").HandlerFunc(UserLogin)
	r.Path("/api/user/signup").Methods("POST").HandlerFunc(UserSignup)
	r.Path("/api/user/{username}/flights").Methods("GET").HandlerFunc(UserFlights)
	r.Path("/api/user/{username}/flights").Methods("POST").HandlerFunc(UserBookFlight)
	r.Path("/api/hotel/{description}/").Methods("GET").HandlerFunc(HotelSearch)
	r.Path("/api/hotel/{description}/{location}/").Methods("GET").HandlerFunc(HotelSearch)

	// Serve our public files out of root
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./public")))

	// Set up our routing
	http.Handle("/", r)

	// Listen on port 8080
	http.ListenAndServe(":8080", nil)
}
