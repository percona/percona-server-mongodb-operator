db = db.getSiblingDB('app');

var bulk = db.city.initializeUnorderedBulkOp();

for (var i=0; i<3000000; i++) {
   bulk.insert({ "name": "city-"+i, "zipcode": i });
}

bulk.execute();
