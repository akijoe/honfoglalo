#!/bin/bash
# Re-downloads the raw source material from the Internet Archive (Wayback Machine)
# and rebuilds the playable files from it. Every file under archive/ came from these captures.
set -e
cd "$(dirname "$0")/.."
W=tools/wb.sh
mkdir -p archive/swf archive/xml
# A főoldali próbajáték kliense (2011, ActionScript 2)
$W 20111029102616 "http://www.honfoglalo.hu/swf/testclient.swf" archive/swf/testclient.swf
# A próbajáték kérdései (gettgquestions.php válasza; a két mentés csak egy válaszban tér el)
$W 20091016153314 "http://www.honfoglalo.hu:80/gettgquestions.php" archive/xml/gettgquestions_20091016.xml
$W 20130425212359 "http://www.honfoglalo.hu:80/gettgquestions.php" archive/xml/gettgquestions_20130425.xml
# Felületszövegek – a kliens a dat/lang.json-t tölti be
$W 20130130151345 "http://www.honfoglalo.hu/dat/lang.json" archive/xml/lang_hu_20130130.json
# A teljes kliens beállításai (2013): innen derül ki, hogy a hangok a client/sounds.swf-ben voltak
$W 20130127114602 "http://www.conquiztador.ru/client/clientparams.xml" archive/xml/clientparams_ru_20130127.xml
# 2004-es teljes kliens (régebbi grafika és hangkészlet, csak forrásanyagnak)
$W 20041230191244 "http://www.honfoglalo.hu:80/client/client3_hu_335.swf?loginid=" archive/swf/client3_hu_335.swf

# a questions.xml-t nem írja felül, ha már van (abba importált kérdések is kerülhettek)
[ -e questions.xml ] || cp archive/xml/gettgquestions_20130425.xml questions.xml
mkdir -p dat && cp archive/xml/lang_hu_20130130.json dat/lang.json
python3 tools/patch_swf.py
