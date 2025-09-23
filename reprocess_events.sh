#!/usr/bin/env bash

# Reprocess events with ZD

jsonFile=$(mktemp)
echo "Using temp file: ${jsonFile}"
moreResults=1
offset=""
while [[ $moreResults -ne 0 ]]; do
	r=$(curl https://msgco.chargebee.com/api/v2/events -s -G -u live_3eWvFcdiKcuuQoponzcdzYGzKn6X79cu7nAT: --data-urlencode sort_by[asc]="occurred_at" \
		--data-urlencode limit=100 --data-urlencode event_type[is]="customer_changed" \
		--data-urlencode occurred_at[between]="[1723447800,1723503600]" --data-urlencode source[is]="api" \
		$offset)
	nextOffset=$(echo $r | jq -r '.next_offset')

	while read -r jrow; do
		user=$(echo $jrow | jq -r '.user')
		# echo $user
		if [[ $user != "Power Automate" ]]; then
			continue
		fi

		# Run this one
		echo "Running:"
		echo jrow
		echo $jrow > $jsonFile
		go run main.go zd_custom_types.go $jsonFile
	
	done < <(echo $r | jq -c '.list[].event')


	if [[ $nextOffset != "" ]]; then
		offset="--data-urlencode offset=${nextOffset}"
	else
		moreResults=0
	fi
done

rm -f $jsonFile