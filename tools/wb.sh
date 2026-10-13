#!/bin/bash
# usage: wb.sh TIMESTAMP URL OUTFILE  -- fetch raw archived capture, retrying on rate limits
for i in 1 2 3 4 5 6; do
  code=$(curl -sL --max-time 120 -w '%{http_code}' "https://web.archive.org/web/${1}id_/${2}" -o "$3")
  if [ "$code" = "200" ] && [ -s "$3" ]; then echo "$3 $(stat -c %s "$3")"; exit 0; fi
  sleep $((i*4))
done
echo "FAIL $3 code=$code" >&2; rm -f "$3"; exit 1
