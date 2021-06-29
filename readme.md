# cyrchdb
A database for web crawling
## Getting started
Assuming you have Go installed, simply `go build .` or `go run .` from within the folder. To stop the database, press enter.
This will save the cache before exiting. If you fail to exit safely, you will have to run `cyrchdb cache-regen` to regenerate the cache.
You will, however need to prepare a seed URL for the whole thing to work. You can do this one of two ways:
### Manually insert
1. Run the database for the first time to generate `results.tsv` and `cache.bin`
2. Stop the database
3. Open `results.tsv` with a text editor using UNIX line endings (LF not CRLF)
4. Add a record by typing a `0` followed by a tab character followed by another `0` followed by a tab character followed by `000` followed by another tab character, and then the URL. You also need to make sure there is a trailing newline character (one blank line at the end)
5. Save the file, then run `cyrchdb cache-regen`
6. Run the database
### API insert
1. Start the database
2. Manually place a POST request as per the instructions below for `POST /introduce`
## `GET /read`
Get an unsearched URL from the database and its index (basically an identifier). Returns a JSON with the `index` (int) and `url` (string)
## `POST /complete`
Mark a URL as having been searched, and update data on whether or not it works, as well as the status returned by the server.
You must pass the index provided when you performed the `GET /read`. Send data as JSON, example below:  
`{ "index":25, "working":true, "status":"200" }`
## `POST /introduce`
Introduce new unsearched URLs into the database. Send them as a JSON array of strings. You do not need to check if these URLs are already in the database;
the database will do that for you (that's the whole point of this database). You also do not need to check for duplicates,
as the database will also remove duplicates in the query. The database will return the number of records it added.
## Results
Results can be found in the `results.tsv` file. The file has no headers, but they would be
1. Done (0 means it has not been searched for URLs, 1 means it has been completed by a crawling program)
2. Working (Arbitrary definition of whether or not it works, as stated in the `/complete` request)
3. Status Code (as provided in `/complete` request)
4. URL
## Notes
- `/read` will hang if there are no records in `results.tsv` with done (column 1) marked as false (0). If a `/introduce` request is placed
while it is hanging (assuming the request actually adds URLs to the database), the `/read` request will stop hanging and provide a response.
- **How do I change the port the server runs on?** Edit the code (lol). Maybe that'll be fixed soon.
- **Can multiple clients access the database at once?** Yes. It would not be very useful otherwise