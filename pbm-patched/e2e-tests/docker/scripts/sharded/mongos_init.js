sh.addShard("rs1/rs101:27017")
sh.addShard("rs1/rs102:27017")
sh.addShard("rs1/rs103:27017")

db.adminCommand({ addShard: "rs2/rs201:27017", name: "rsx" });
db.adminCommand({ addShard: "rs2/rs202:27017", name: "rsx" });
db.adminCommand({ addShard: "rs2/rs203:27017", name: "rsx" });
