package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/nukosuke/go-zendesk/zendesk"
	"github.com/spf13/viper"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	viper.SetDefault("DEBUG", false)
	viper.AutomaticEnv()
	replacer := strings.NewReplacer(" ", "_")
	viper.SetEnvKeyReplacer(replacer)

	zd, err := zendesk.NewClient(nil)
	if err != nil {
		log.Fatal(err.Error())
	}
	zd.SetSubdomain(viper.GetString("ZD API DOMAIN"))
	zd.SetCredential(zendesk.NewAPITokenCredential(viper.GetString("ZD API USER"), viper.GetString("ZD API TOKEN")))

	searchTerms := zendesk.SearchUsersOptions{
		PageOptions: zendesk.PageOptions{PerPage: 2}, // any more than 1 is an error state
	}
	if len(os.Args) > 1 {
		searchTerms.Query = "type:user email:\"" + os.Args[1] + "\""
	} else {
		log.Fatal("not supported")
	}
	zdUsers, _, err := zd.SearchUsers(context.Background(), &searchTerms)
	if err != nil {
		log.Fatal(err.Error())
	}

	for _, u := range zdUsers {
		r, err := zd.Get(context.TODO(), "/end_users/"+strconv.FormatInt(u.ID, 10)+"/identities")
		if err != nil {
			log.Fatal(err)
		}
		var userIdentities zdUserIdentities
		err = json.Unmarshal(r, &userIdentities)
		if err != nil {
			log.Fatal(err)
		}
		for _, i := range userIdentities.Identities {
			if strings.EqualFold(i.Type, "phone_number") {
				noSpPhone := strings.Join(strings.Fields(i.Value), "")
				log.Printf("nosp id: %s, user.phone: %s\n", noSpPhone, u.Phone)
				// if noSpPhone == u.Phone {
				// 	log.Printf("Deleting ID: %s\n", i.URL)
				// 	// err = zd.Delete(context.TODO(), "/end_users/"+strconv.FormatInt(u.ID, 10)+"/identities/"+strconv.FormatInt(i.ID, 10))
				// 	// if err != nil {
				// 	// 	log.Fatal(err)
				// 	// }
				// } else
				if strings.HasPrefix(u.Phone, "+61") && noSpPhone[0] == '0' && u.Phone[3:] == noSpPhone[1:] {
					log.Printf("Deleting ID: %s\n", i.URL)
					// err = zd.Delete(context.TODO(), "/end_users/"+strconv.FormatInt(u.ID, 10)+"/identities/"+strconv.FormatInt(i.ID, 10))
					// if err != nil {
					// 	log.Fatal(err)
					// }
				}
			}
		}
	}
}
