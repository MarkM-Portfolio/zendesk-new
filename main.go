package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/chargebee/chargebee-go/v3/models/customer"
	"github.com/chargebee/chargebee-go/v3/models/event"
	"github.com/nukosuke/go-zendesk/zendesk"
	"github.com/spf13/viper"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var credentials string
var title cases.Caser
var conf *viper.Viper

func handler(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {

	if conf.GetBool("DEBUG") {
		log.Printf("CONTEXT: %+v\n", ctx)
		log.Printf("REQUEST: %+v\n", request)
		log.Println("Environment:")
		for _, v := range os.Environ() {
			eqIdx := strings.IndexRune(v, '=')
			if strings.Contains(v[0:eqIdx], "PASS") {
				log.Printf("\t%s=<masked>\n", v[0:eqIdx])
			} else {
				log.Printf("\t%s\n", v)
			}
		}
	}

	// Check the call is in order
	authOK := false
	contentTypeOK := false
	if conf.GetBool("DEBUG") {
		log.Println("HTTP Request Headers:")
	}
	for key, value := range request.Headers {
		if conf.GetBool("DEBUG") {
			log.Printf("\t%s: %s\n", key, strings.TrimRight(value, "\n"))
		}
		if strings.EqualFold(key, "authorization") && value == "Basic "+credentials {
			authOK = true
		} else if strings.EqualFold(key, "content-type") && strings.Contains(strings.ToLower(value), "application/json") {
			contentTypeOK = true
		}
	}
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" && !authOK {
		errString := "authentication failed"
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusUnauthorized, Body: errString}, nil
	}
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" && !contentTypeOK {
		errString := "invalid content"
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: errString}, nil
	}

	// What was the event?
	var cbEvent event.Event
	err := json.Unmarshal([]byte(request.Body), &cbEvent)
	if err != nil {
		errString := "invalid request: " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: errString}, nil
	}
	log.Printf("Processing event: %s\n", cbEvent.Id)

	// We are assuming this webhook will only be called when we have some data to action, let's go see...
	cbContent := map[string]interface{}{}
	err = json.Unmarshal([]byte(cbEvent.Content), &cbContent)
	if err != nil {
		errString := "invalid request: " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: errString}, nil
	}
	cb, ok := cbContent["customer"]
	if !ok {
		errString := "invalid request (no customer)"
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: errString}, nil
	}
	jsonData, err := json.Marshal(cb)
	if err != nil {
		errString := "invalid request (jsonData): " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: errString}, nil
	}
	var cbCustomer customer.Customer
	err = json.Unmarshal(jsonData, &cbCustomer)
	if err != nil {
		errString := "invalid request (cbCustomer): " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusBadRequest, Body: errString}, nil
	}
	if conf.GetBool("DEBUG") {
		log.Printf("cbCustomer: %+v\n", cbCustomer)
	}
	fullName := title.String(cbCustomer.FirstName) + " " + title.String(cbCustomer.LastName)
	if len(strings.TrimSpace(fullName)) <= 0 {
		// this is just an initial user registration, so no action needed right now
		log.Println("No action: %s\n", cbEvent.Id)
		return events.LambdaFunctionURLResponse{Body: "No Action", StatusCode: http.StatusOK}, nil
	}

	// We have a customer record, let's make sure Zendesk is in sync - lookup a user, and if they don't exist we will
	// create them.  If their details are incorrect, then let's update them in ZD.
	zd, err := zendesk.NewClient(nil)
	if err != nil {
		errString := "unable to initialise zendesk: " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
	}
	zd.SetSubdomain(conf.GetString("ZD_DOMAIN"))
	zd.SetCredential(zendesk.NewAPITokenCredential(conf.GetString("ZD_API_USER"), conf.GetString("ZD_API_TOKEN")))

	// First try the extenal id
	searchKey := cbCustomer.Id
	zdUsers, _, err := zd.SearchUsers(context.Background(), &zendesk.SearchUsersOptions{
		PageOptions: zendesk.PageOptions{PerPage: 1}, // any more than 1 is an invalid state
		Query:       searchKey,
	})
	if err != nil {
		errString := "zendesk external-id search failed: " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
	}

	// If the email is different from the one we are looking for, then we might need to do a merge first.
	if len(zdUsers) > 0 && zdUsers[0].Email != cbCustomer.Email {

		// So the external id found user has a different email, this is because the email needs to be updated
		// or we have a split personality - if we find another user with the same email address let's merge them.
		searchKey = cbCustomer.Email
		zdUsersByEmail, _, err := zd.SearchUsers(context.Background(), &zendesk.SearchUsersOptions{
			PageOptions: zendesk.PageOptions{PerPage: 2}, // any more than 1 is an error state
			Query:       searchKey,
		})
		if err != nil {
			errString := "zendesk search failed: " + err.Error()
			log.Printf("ERROR: %s\n", errString)
			return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
		}
		if len(zdUsersByEmail) > 1 {
			errString := "too many zendesk records for search key: " + searchKey
			log.Printf("ERROR: %s\n", errString)
			return events.LambdaFunctionURLResponse{StatusCode: http.StatusConflict, Body: errString}, nil
		}
		if len(zdUsersByEmail) > 0 && zdUsersByEmail[0].ID != zdUsers[0].ID {
			// OK, we have a split personality - let's merge them together.  Annoyingly the ZD module we are using
			// doesn't have a merge function, so we will need to do this manually.
			err = zdMergeIdIntoId(zdUsersByEmail[0].ID, zdUsers[0].ID)
			if err != nil {
				errString := "failed to merge id '" + strconv.FormatInt(zdUsersByEmail[0].ID, 10) + "' into '" + strconv.FormatInt(zdUsers[0].ID, 10) + "'"
				log.Printf("ERROR: %s\n", errString)
				return events.LambdaFunctionURLResponse{StatusCode: http.StatusConflict, Body: errString}, nil
			}
		}
	} else if len(zdUsers) <= 0 {
		// Did not find any with the right external ID, how about by email?
		searchKey = cbCustomer.Email
		zdUsers, _, err = zd.SearchUsers(context.Background(), &zendesk.SearchUsersOptions{
			PageOptions: zendesk.PageOptions{PerPage: 2}, // any more than 1 is an error state
			Query:       searchKey,
		})
		if err != nil {
			errString := "zendesk search failed: " + err.Error()
			log.Printf("ERROR: %s\n", errString)
			return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
		}
		if len(zdUsers) > 1 {
			errString := "too many zendesk records for search key: " + searchKey
			log.Printf("ERROR: %s\n", errString)
			return events.LambdaFunctionURLResponse{StatusCode: http.StatusConflict, Body: errString}, nil
		}
	}
	if conf.GetBool("DEBUG") {
		log.Printf("zdUsers: %+v\n", zdUsers)
	}

	// create or update the user if necessary
	newUser := zendesk.User{
		Name:       fullName,
		Active:     true,
		ExternalID: cbCustomer.Id,
		Verified:   true,
		// SkipVerifyEmail: true,
	}
	cbPhone := strings.Join(strings.Fields(cbCustomer.Phone), "")
	if cbPhone != "" {
		newUser.Phone = cbPhone
	}
	if conf.GetBool("DEBUG") {
		log.Printf("newUser: %+v\n", newUser)
	}
	actionUser := false
	if len(zdUsers) < 1 && len(strings.TrimSpace(fullName)) > 0 {
		if conf.GetBool("DEBUG") {
			log.Println("User will be created.")
		}
		newUser.Email = cbCustomer.Email
		actionUser = true
	} else {
		// can only be one
		newUser.ID = zdUsers[0].ID
		if len(strings.TrimSpace(fullName)) > 0 && (zdUsers[0].Name != fullName ||
			(cbPhone != "" && !strings.EqualFold(zdUsers[0].Phone, cbPhone)) ||
			!strings.EqualFold(zdUsers[0].ExternalID, cbCustomer.Id) ||
			!zdUsers[0].Verified ||
			!zdUsers[0].Active) {
			if zdUsers[0].Role != "admin" && zdUsers[0].Role != "agent" {
				actionUser = true
			}
		}
	}
	if conf.GetBool("DEBUG") {
		log.Printf("actionUser: %+v\n", actionUser)
	}
	if actionUser {
		newUser, err = zd.CreateOrUpdateUser(context.Background(), newUser)
		if err != nil {
			errString := "zendesk create failed: " + err.Error()
			log.Printf("ERROR: %s\n", errString)
			return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
		}
	}

	// Cleanup any useless identities
	zdResult, err := zd.Get(context.TODO(), "/end_users/"+strconv.FormatInt(newUser.ID, 10)+"/identities")
	if err != nil {
		errString := "zendesk identity search failed: " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
	}
	var userIdentities zdUserIdentities
	err = json.Unmarshal(zdResult, &userIdentities)
	if err != nil {
		errString := "json unmarshal failed: " + err.Error()
		log.Printf("ERROR: %s\n", errString)
		return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
	}
	for _, identity := range userIdentities.Identities {
		if strings.EqualFold(identity.Type, "phone_number") {
			noSpPhone := strings.Join(strings.Fields(identity.Value), "")

			// remove any phone numbers that are not the same as chargebee
			if cbPhone != "" && noSpPhone != cbPhone {
				log.Printf("Deleting Identity: %s\n", identity.URL)
				err = zd.Delete(context.TODO(), "/end_users/"+strconv.FormatInt(newUser.ID, 10)+"/identities/"+strconv.FormatInt(identity.ID, 10))
				if err != nil {
					errString := "json unmarshal failed: " + err.Error()
					log.Printf("ERROR: %s\n", errString)
					return events.LambdaFunctionURLResponse{StatusCode: http.StatusInternalServerError, Body: errString}, nil
				}
			}
		}
	}

	log.Printf("Success: %s\n", cbEvent.Id)
	return events.LambdaFunctionURLResponse{Body: "OK", StatusCode: http.StatusOK}, nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	conf = viper.New()
	conf.SetDefault("DEBUG", false)
	conf.AutomaticEnv()
	replacer := strings.NewReplacer(" ", "_")
	conf.SetEnvKeyReplacer(replacer)
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") == "" {
		conf.SetConfigName("dev")
		conf.SetConfigType("yaml")
		conf.AddConfigPath(".")
		if err := conf.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				// no config file, that's ok
			} else {
				log.Fatal(err)
			}
		} else {
			log.Println(conf)
			conf = conf.Sub("resources.dev.properties.environment.variables")
			if conf == nil {
				log.Fatal("unable to use dev.yaml environment vars as config")
			}
		}
	}

	title = cases.Title(language.BritishEnglish)
	credentials = base64.StdEncoding.EncodeToString([]byte(conf.GetString("WEBHOOK_USERNAME") + ":" + conf.GetString("WEBHOOK_PASSWORD")))

	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		lambda.Start(handler)
	} else {

		if len(os.Args) <= 1 {
			log.Fatal("no files provides as arguments")
		}

		for _, fname := range os.Args[1:] {
			log.Printf("Processing: %s\n", fname)
			b, err := os.ReadFile(fname)
			if err != nil {
				log.Fatal(err)
			}

			r, err := handler(context.TODO(), events.LambdaFunctionURLRequest{Body: string(b)})
			if err != nil {
				log.Fatal(err)
			}
			log.Println(r)
		}
	}
}
