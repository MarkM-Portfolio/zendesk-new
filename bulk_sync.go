package main

import (
	"context"
	"fmt"
	"log"

	"github.com/chargebee/chargebee-go/v3"
	customerAction "github.com/chargebee/chargebee-go/v3/actions/customer"
	"github.com/chargebee/chargebee-go/v3/filter"
	"github.com/chargebee/chargebee-go/v3/models/customer"
	"github.com/nukosuke/go-zendesk/zendesk"
	"github.com/spf13/viper"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	// "os"
	"strings"
	// "time"
)

var credentials string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	viper.SetEnvPrefix("TMC")
	viper.SetDefault("DEBUG", false)
	viper.AutomaticEnv()
	replacer := strings.NewReplacer(" ", "_")
	viper.SetEnvKeyReplacer(replacer)

	// Configure CB
	chargebee.Configure(viper.GetString("CB_KEY"), viper.GetString("CB_SITE"))

	// Find the TPG associated users
	// tmStart, err := time.Parse(time.RFC822Z, os.Args[1])
	// if err != nil {
	// 	log.Fatal("invalid time string in argv[1]")
	// }
	listRequest := customer.ListRequestParams{
		// CreatedAt: &filter.TimestampFilter{
		// 	After: tmStart.UTC().Unix(),
		// },
		Email:  &filter.StringFilter{Is: "lennart_lovdin@internode.on.net"},
		Limit:  chargebee.Int32(100),
		SortBy: &filter.SortFilter{Asc: "created_at"},
	}
	offset := ""
	chCustomers := make(chan customer.Customer, 200)
	go func() {
		for moreResults := true; moreResults; {
			if offset != "" {
				listRequest.Offset = offset
			}
			res, err := customerAction.List(&listRequest).ListRequest()
			if err != nil {
				log.Fatal(err)
			}
			if res.NextOffset != "" {
				offset = res.NextOffset
			} else {
				moreResults = false
			}
			// moreResults = false

			for _, r := range res.List {
				// fmt.Printf("customer: %+v\n", r)
				chCustomers <- *r.Customer
			}
		}

		close(chCustomers)
	}()

	zd, err := zendesk.NewClient(nil)
	if err != nil {
		log.Fatal(err.Error())
	}
	zd.SetSubdomain("themessagingcompany")
	zd.SetCredential(zendesk.NewAPITokenCredential(viper.GetString("ZD API USER"), viper.GetString("ZD API TOKEN")))
	title := cases.Title(language.BritishEnglish)

	for customer := range chCustomers {
		if !strings.HasSuffix(customer.Email, "atmail.com") {

			fmt.Printf("processing: %s ", customer.Email)

			// We have a customer record, let's make sure Zendesk is in sync - lookup a user, and if they don't exist we will
			// create them.  If their details are incorrect, then let's update them in ZD.
			zdUsers, _, err := zd.SearchUsers(context.Background(), &zendesk.SearchUsersOptions{
				PageOptions: zendesk.PageOptions{PerPage: 2}, // any more than 1 is an error state
				Query:       "type:user email:\"" + customer.Email + "\"",
			})
			if err != nil {
				log.Fatal(err.Error())
			}
			if len(zdUsers) > 1 {
				log.Fatal("too many zendesk records for user: " + customer.Email)
			}

			// create or update the user if necessary
			newName := title.String(customer.FirstName) + " " + title.String(customer.LastName)
			fmt.Printf("(%s) => ", newName)
			newUser := zendesk.User{
				Email:      customer.Email,
				Name:       newName,
				Active:     true,
				ExternalID: customer.Id,
				Verified:   true,
				// SkipVerifyEmail: true,
			}
			if customer.Phone != "" {
				newUser.Phone = customer.Phone
			}
			// log.Printf("newUser: %+v\n", newUser)
			fmt.Printf(", zdPhone: '%s', cbPhone: '%s' ", zdUsers[0].Phone, customer.Phone)
			actionUser := false
			if len(zdUsers) < 1 && len(strings.TrimSpace(newName)) > 0 {
				actionUser = true
			} else {
				// can only be one
				if len(strings.TrimSpace(newName)) > 0 && (zdUsers[0].Phone != customer.Phone ||
					zdUsers[0].Name != newName ||
					!strings.EqualFold(zdUsers[0].ExternalID, customer.Id) ||
					!zdUsers[0].Verified ||
					!zdUsers[0].Active) {
					actionUser = true
				}
			}
			fmt.Println(actionUser)
			if actionUser {
				newUser, err = zd.CreateOrUpdateUser(context.Background(), newUser)
				if err != nil {
					log.Fatal("zendesk create failed: " + err.Error())
				}
			}
		} else {
			fmt.Printf("not processing: %s\n", customer.Email)
		}
	}
}
