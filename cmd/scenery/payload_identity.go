package main

// cliPayloadIdentity identifies command-specific data inside the singular CLI
// envelope. Specification and producer identity live on the outer envelope.
type cliPayloadIdentity struct {
	Kind           string `json:"kind"`
	SchemaRevision string `json:"schema_revision"`
}

var cliPayloadSchemaRevisions = map[string]string{
	"scenery.help":                        "sha256:b2d01675a4f18a153fc6ab90070bd32c757174a0889532b3c33210b09b7ce808",
	"scenery.agent_context":               "sha256:9c67d27f74dcfef4ae20c8a017e38efabee8b482de9f3294fd47d38a7d0338d6",
	"scenery.db.apply.result":             "sha256:8e9223ebba90b7f493b14f0bbc0347da0ea53d0bc5769ad42e080c99f73b71a4",
	"scenery.db.list":                     "sha256:01b0f8b9cbd30278207c2e5b3b2ae00a9af81ff624e27c3bfc2995e826598e15",
	"scenery.db.seed.result":              "sha256:38c33fe664ee1c2f8122d07188546cc8768192bbc984af6b1ca2e3c4e2cf7765",
	"scenery.db.server.status":            "sha256:120bed92b10ab3cd646f455e7f1492d9c7b2b0cf081a32c7b37e859504171621",
	"scenery.db.server.stop":              "sha256:f3e444f3a3840938fa93468976a7cbf9c260a97f163075c3e5c59b333667bd2f",
	"scenery.db.setup.result":             "sha256:2cad136856380c3a97e2e8e1dd7cc4cfff94346baaab57199b1a7630820463d1",
	"scenery.agent.restart":               "sha256:a39bcc3f6c59675582af60bbbcf10d2e7f65c2c66a43088eec60ba4628c16258",
	"scenery.agent.status":                "sha256:5a50e5ca784a3ac4552b7d77287dee3753a71b198751dad0a3978e451a92a303",
	"scenery.doctor.deploy":               "sha256:610e6bd45da2f06e70966c648d9b8a9cc072d4eea9d51fe73aa857c3d45ef961",
	"scenery.doctor.result":               "sha256:1c9484e483506f4a9a82a134e5f4a84e6664166b4051cfe9bbe680d477bedaa6",
	"scenery.down":                        "sha256:650b1258f522f112e9f91c1114af6affc47e4dde0470dd188b0124454446b826",
	"scenery.traces.clear":                "sha256:74017cd28d7ebf63756c18c4e3ae477ef6e87485b9d153b492513497dace7ca5",
	"scenery.inspect.build":               "sha256:5787ddfb761b34a352c20b45dc37f50a85356824ecb7579d00abe36a87e65572",
	"scenery.inspect.generators":          "sha256:0269eee33879a07dc9ecd83d78180fe8e3276f4d948506740bc2586e91625f55",
	"scenery.inspect.validation":          "sha256:183db6b1f50ba7358e8eeb5466e30baab240e79bf7858a507367855b415305e0",
	"scenery.validation.list":             "sha256:9aa2c891d0e84b0b133a0cae81a04b4d7bd0706a1d072f0807a1429b533956a6",
	"scenery.validation.inspect":          "sha256:cebd1d3580ec6f91147dc707bca344abbb5d20787749fd5dd28e76bdf480b085",
	"scenery.validation.graph":            "sha256:35965ecbaf060d0b8256d797246367e8758c5a8b321d43ee54423a0cb4932fef",
	"scenery.validation.plan":             "sha256:0e5f44a4dd200d6d6e648367b9503ef9dc805f7dc3576420d79ac51e96701067",
	"scenery.validation.result":           "sha256:ed8954775be56b4fbcf7d87e97a3e6bd75eadeefdc16893c3765fc5ac3bf04c3",
	"scenery.inspect.app":                 "sha256:b03f14d1a3f67697f8a3f410bb037a43b6bd02e9119cd395466278fe6a21ea55",
	"scenery.inspect.services":            "sha256:eed0de0f1862bd98948f299b756a92df5f13532b1f1d71df5dd908cf33aa0c28",
	"scenery.inspect.routes":              "sha256:000c440270d6bfe7bde83bbba963e0a4614aa830b284c4fca8424e82c1ca5bd0",
	"scenery.inspect.endpoints":           "sha256:af1066b46918c1a19a1e24c22e7316c35a406a6d8cfdd04c49e1a1623a797d13",
	"scenery.inspect.observability":       "sha256:d4a30b220fd68c3155a257fdbfdece6d58ecf9f6c851fa569f7eacfe9ed7f5aa",
	"scenery.inspect.durable":             "sha256:2e767f9bda8f938a8ef91a1c386f509dd1aafd7ae032d6e56ed14e79fca35c56",
	"scenery.inspect.docs":                "sha256:a13fe6effd00df3811d1cf3d163c9898503f09f7adae939b075b64eb9789996a",
	"scenery.inspect.paths":               "sha256:608b88133556842c287301f9d5dc62e97e76afec107695192b272d2fd6896d38",
	"scenery.inspect.metrics":             "sha256:6af4d264dbb1fd08f82a3b69eac6114dd400100145476bae5f8cdd2fb8f337bd",
	"scenery.inspect.traces":              "sha256:f3a83468f7bc3d018825b0536c47515a3d6e5e9d053a3e3442a6fa3a6a9bd816",
	"scenery.inspect.ui":                  "sha256:16c903f6ec1b17894cd23150bd387876af888fb957cabe010f1ba9219a24cb50",
	"scenery.logs.query":                  "sha256:d863e3f48e9645e48e8fd1188f50e11a180da452e771868b378d6e5a170cf69b",
	"scenery.logs.tail.entry":             "sha256:17abdfc0a0061a4e2d712621561b7dfb90e443a64beeba86de111ba2e7db887a",
	"scenery.metrics.query":               "sha256:59ed52449eeb10a9ebcba329f6399917ef3bf1d425ab9d3c4eacaebed730ed6b",
	"scenery.metrics.labels":              "sha256:3ada3931f5afdb8a340d4fc2318613297632bae6646c2218df34ec3b98d22482",
	"scenery.metrics.series":              "sha256:ccb53b231affc674aa36da784d17aa57f6ed5d9425127327ddace8983d00bb39",
	"scenery.inspect.harness":             "sha256:f85ff889bd47c12fef97c8f922a235989ad736775207a7cf6c2e24a5d48e4897",
	"scenery.harness.artifact":            "sha256:5fdbd3fbabd171b9226331c8d821c2a59744e7682943593896c332b8ac69eaa8",
	"scenery.harness.changed_area":        "sha256:c215f07dddb5b11acca4d74c93159b1506aa0c13321bbf6b1af5d9669984bb9e",
	"scenery.harness.drift":               "sha256:2218980db0b2af538f8773e3a3412f1a15c2d0c66ef6574bd8910805b8eebf71",
	"scenery.harness.fixture_matrix":      "sha256:0317781d4d92e84a88e03e368cc06c8371a325ac37d48ca8982dbb70eeefb729",
	"scenery.harness.result":              "sha256:19f4764781db12ff44ec727ecfff77b0a633a238ad4b316323c862288f54afc6",
	"scenery.harness.schema_validation":   "sha256:0b8037ff35c8ad553fa023abf7886d3fe7e28649400fa8d69d3a7994062d91bd",
	"scenery.harness.self":                "sha256:eb4b9f75f356960f0b857cada85330979a7f9a9d31ec8f3c48b12dbcd40b8b08",
	"scenery.harness.self.summary":        "sha256:ca2ec5d5f851b1b3f060b4fca17e63b656b40fcd54d5d89762b12280626bc050",
	"scenery.harness.test_timing":         "sha256:c0e41b1643dd776e82331cd0e1ba8048446a398f4f0a482e092ed11e97cbaf3b",
	"scenery.harness.toolchain":           "sha256:edb5bd2a880e5ed8d87baa7906437520766479ffe7f9a26d754ce35c3656e3a3",
	"scenery.harness.ui":                  "sha256:32aa15a9c70fd00ed2f701ea74d1f3b3eaf3ce8c20a05677a83fbb23404a64ef",
	"scenery.harness.ui.dom":              "sha256:39eb0887de5fc2851fc76fa4617bbf09eedddb732331819a26abf73d2776fb9c",
	"scenery.build.result":                "sha256:222cea7f47a8ed1776bdfec77b9c8a499f636840b22b1c72cf4260059b4212e9",
	"scenery.run.event":                   "sha256:acd3c69b8de91403ea35c71807d38e492bfb4a1cf23242be9a4475b10c178c6a",
	"scenery.dev.event":                   "sha256:88d7bd9bd7e38e93de754e8508bde933e9d49d48a5391c29686e11737bb5ba2e",
	"scenery.dev.detach":                  "sha256:3ba4e45cfd425a451b8a6caf3d7217014fc9d48055d00c9a158a500a255f1742",
	"scenery.deploy.target":               "sha256:356e55ad96ebecf480ec923f0f57278a7b1d670050cbdae6fdc8319633ab26f6",
	"scenery.deploy.setup":                "sha256:13031efa37d9cb702b60fabe94e51536439b1d3796313e62e45a151e2a581c1e",
	"scenery.deploy.resume":               "sha256:bed17db79b44a0b8508898e8db3db4ae91b9239d9bf3f0b5d9afa3b3f234f939",
	"scenery.deploy.teardown":             "sha256:5f4c80e767cd9fc8d8b32f2cd330d7248c917dd3c3e672d95d20294779139aa4",
	"scenery.deploy.status":               "sha256:44aec24f02fc8aabe19d395a08ba9fb40ebcb244e6acd90fbc5c113fd0eee529",
	"scenery.deploy.publish":              "sha256:e955649b692bd4fb98e3f5df375f74a62d447bd4e8818f2f55713214051a57c0",
	"scenery.edge.trust":                  "sha256:5b028667b235c96636e9f3dd51f311600201534cd3bfe8d47afdf084c233eabe",
	"scenery.edge.dns.status":             "sha256:041b8530c0083c8928023de9539c15a4ff92626cf1b186626105731fe01e446f",
	"scenery.edge.status":                 "sha256:f3078edc6896e0e859339cc97443e533e8e622f3a4feed6b658488263e0cc467",
	"scenery.storage.inspect":             "sha256:4765e31dc934ac88a1c8e84cc49da83e3863352a0056f6a756cca36c6e8972d4",
	"scenery.storage.cleanup":             "sha256:b794cf14c0dbe58e5601384929f93e1a3125d44c181afc2444c6870d7a31d5dc",
	"scenery.storage.delete":              "sha256:a7c00ff140a9db27999660049545588ca8a5521e1956b36823a8c948eea74adb",
	"scenery.storage.list":                "sha256:5efdf0ca16bbcf0b7c7534c14fd984a09384aea90518b75be64dd9bdba8a4eb7",
	"scenery.storage.object":              "sha256:f7c4e21d85a92097f91347b8d9861c00f3042a40102a677ea79c080921a9ec2a",
	"scenery.storage.status":              "sha256:8f70ca219f68fdcd348256e2a9dd54731778aa3304d34ade3adc347c93061dd0",
	"scenery.storage.webui":               "sha256:039750815e774c0f89b25c418df225efa8f0e32fcf50bfb1beb610c7253f0254",
	"scenery.snapshot.save":               "sha256:2b96edbc636dc4810f4cd13a15ed7ec9703c5ba78e5d7ee5406bbd38fcf1110a",
	"scenery.snapshot.verify":             "sha256:987c278f197ef24ba46d2123574f9e356cea37132c22d15d1a82d9cc309473ab",
	"scenery.snapshot.load":               "sha256:c4bfe6cf0b695861f9a82f98b8f969d71e8e3a5ff05e6572fec977f3507e5b1f",
	"scenery.task.graph":                  "sha256:6897eba81ccdf4dc44c1c4d859f2c9e4bea6a4b49bf8fe52df07bec5c111f3d4",
	"scenery.task.inspect":                "sha256:3adcce0faa04b452008200ea56d5c10642db57cd4980ef807d2e2b459e8495fc",
	"scenery.task.list":                   "sha256:7cbb5e4355f533e353de7e3171a2b3cb4b60c30675d884ca52733295f89a4c99",
	"scenery.durable.worker_token.create": "sha256:f7796e7a49f33ebaa8a3304439f08de48d2391d31bcd1efbb735ac6ac8da8520",
	"scenery.durable.jobs":                "sha256:e680c8870c213d3fba5b8aa35fc7b9fda71e95fc526330f529c108728519f948",
	"scenery.upgrade":                     "sha256:6022bbea99526a7a838bfe887dd1cd8ffb27fe49e0aefa6827dc4c2b5c03cf57",
	"scenery.version":                     "sha256:7b2f75aa63a70fdeec0ef99ff740321fcd4cebcb2b45f0e177ee4884519a4fa9",
	"scenery.worktree.create":             "sha256:7d2585e9045317d17a142fd91ceb4b38d0f60f8223abc31d30736697424fa5ea",
	"scenery.worktree.list":               "sha256:2d42d90ddd63a94c1e0fe12a1cf04023b7ccc167534c32751dc06a2714aed237",
	"scenery.worktree.remove":             "sha256:c9a11366bd39db3ae470555a433ecb93d82459f38ef0f645cd2c0fe456f08131",
}

func newCLIPayloadIdentity(kind string) cliPayloadIdentity {
	revision, ok := cliPayloadSchemaRevisions[kind]
	if !ok {
		panic("missing CLI payload schema revision for " + kind)
	}
	return cliPayloadIdentity{Kind: kind, SchemaRevision: revision}
}

func withCLIPayloadIdentity(kind string, values map[string]any) map[string]any {
	identity := newCLIPayloadIdentity(kind)
	values["kind"] = identity.Kind
	values["schema_revision"] = identity.SchemaRevision
	return values
}
