try-cb-golang
===============

This is a sample application for getting started with Couchbase Server 6.5 or later, using collections. 
The application runs a single page UI and demonstrates SQL for Documents (N1QL) and Full Text Search (FTS) querying capabilities.
It uses Couchbase Server 6.5.0 together with Go, Vue and Bootstrap.

The application is a flight planner that allows the user to search for and select a flight route (including the return flight) based on airports and dates.
Airport selection is done dynamically using an autocomplete box bound to N1QL queries on the server side.
After selecting a date, it then searches for applicable air flight routes from a previously populated database.
An additional page allows users to search for Hotels using less structured keywords.

## Prerequisites
The following pieces need to be in place in order to run the application.

1. Couchbase Server 6.5.0
2. Go 1.13+

## Installation and Configuration
To run the application, you first need to install Couchbase.
You will need at least version 6.5.0, running the kv, query, and search services.

Once you have installed Couchbase, you will need to enable the travel-sample bucket.
You can do this from the Settings/Sample Buckets tab.

You also need to create a text search index, to allow the application to search for hotels. 
In the Search tab, create an index for the travel-sample bucket named "hotels" with a type mapping for type "hotel".
Leave all other properties of the index at defaults.
Wait for the "indexing progress" to reach 100%.

Next you need to enable developer preview on 6.5.0.
Note that [developer preview](https://docs.couchbase.com/server/current/developer-preview/preview-mode.html) is not for use in production and cannot be disabled once enabled.
You can enable it by doing:
```bash
/opt/couchbase/bin/couchbase-cli enable-developer-preview --enable -c localhost:8091 -u Administrator -p password
```

Now run the included `create-collections.sh` script to set up the correct collections.
Note that this script will also create a new bucket requiring the cluster to have 100MB of RAM spare.
 ```bash
 ./create-collections.sh Adminstrator password localhost
 ```

Now that Couchbase Server is running, we have our buckets, and we have our collections we can start using the travel application.

First we need to clone this repository

 ```bash
 git clone https://github.com/couchbaselabs/try-cb-golang.git
 ```

Now we can run the application

 ```bash
 cd try-cb-golang/
 go run main.go
 ```

 Open a browser and load the url http://localhost:8080

## REST API DOCUMENTATION
The REST API for this example application can be found at:
[https://github.com/couchbaselabs/try-cb-frontend/blob/master/documentation/try-cb-api-spec-v2.adoc](https://github.com/couchbaselabs/try-cb-frontend/blob/master/documentation/try-cb-api-spec-v2.adoc)
