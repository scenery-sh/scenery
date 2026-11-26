package machine

// cliPayloadIdentity identifies command-specific data inside the singular CLI
// envelope. Specification and producer identity live on the outer envelope.
type PayloadIdentity struct {
	Kind           string `json:"kind"`
	SchemaRevision string `json:"schema_revision"`
}

var payloadSchemaRevisions = map[string]string{
	"scenery.add.module.result":         "sha256:8a2c549600d635d4a2cbfac7b50220b8bbe65e29686e8c25802aade225733d11",
	"scenery.durable.run.result":        "sha256:52be1b17481ad2e3b0b97647802b9c76959c80e0ae792aab927dbb90c0040f5c",
	"scenery.db.query.result":           "sha256:0ac6740ae588d177b3789c216b2e3ea81a085fa4b7336a6f5b229d82c7465ecc",
	"scenery.test.result":               "sha256:d10e7c5c2090bfdfef132ef89233d788e106d93f000c7c6a3f76c031c669d511",
	"scenery.context":                   "sha256:8741784f13af8c758e8afe7ccd427d1e88a59fc71ccae265a7ab85618f711bd4",
	"scenery.call.result":               "sha256:b6430d42ccbb5f3d5e8e3790bcdf65549a3c7070028c27a2b048ff32883e2e48",
	"scenery.feature":                   "sha256:1986097fb2b2a03fb11d3fa83bba5bf3485664b58ec20439ce3f7ffc572069dd",
	"scenery.harness.verification":      "sha256:0b17c6f9d5e265127a577656afb8e61b39462dcb5dec09042a015c9bd79bac8a",
	"scenery.harness.test_results":      "sha256:942678c71de92263ea889e580240c1bd68fbb90b33abda846c4b3fd534416e7c",
	"scenery.agent_context.summary":     "sha256:5ae84637586fefca4ff0daed4cde13311e86b03a7cf98a36ccda409ee73e0295",
	"scenery.db.migrate":                "sha256:0f400279c74205778e4d2656af9295ce659ce08b93a7502e8dcfb41dd79d81c4",
	"scenery.config.show":               "sha256:a98b049f8eb2fc74c8bd92be9d15d028746b4c3fb0b38d453859cc2d33e828a0",
	"scenery.config.change":             "sha256:02c8d5305d3a5c7fed8657e41e0722f5245b0dbab69666e371e6badc58d9a71c",
	"scenery.framework":                 "sha256:69a8bb90531001ad4fa884544fb666f56adec701957f6ef642eb531b6878b6f4",
	"scenery.help":                      "sha256:0a6845da958e8576a6a281c8a563b31fa81fba7f4bf489dbe2a5180d27305a9f",
	"scenery.agent_context":             "sha256:061aeab809775c884cf58810b69ed7f4fcc5d55753ae810bbc72a362d8a272ff",
	"scenery.db.list":                   "sha256:f896a0e3a62155d6e8017ef85f87b0892ea43a8d21e3b79bb1aaa73c877275fc",
	"scenery.db.seed.result":            "sha256:891700972b16d29b03f8f57e819174a4fb187371d0f60aaf4df624b52b3de005",
	"scenery.db.server.status":          "sha256:859372bc8b167d297b8b14fc48e912a66d9d09485749ddff366c1ef4d9f43ba9",
	"scenery.db.server.stop":            "sha256:f6a01935d0175d9b5a0786f52675641b94b06b21329ca751773328c290a08e34",
	"scenery.db.setup.result":           "sha256:549cc09d6b260f0f1f0bc47f12c1d0949b33fe72c27963b4558624083c948995",
	"scenery.agent.restart":             "sha256:604b1e57a02c5fd35d6d3a4acea2fe6bf182b5fbe7580e465f93e7f6288c143d",
	"scenery.agent.supervisor":          "sha256:07a7cacacb0faad6efca997f642ad7fb05abac973be7974b58eb2029b31edcb4",
	"scenery.agent.status":              "sha256:71360b94216a58c152fd8c4dfc94d3a31c31ebeaec6761ece15486808a947910",
	"scenery.doctor.result":             "sha256:610e01220961d047b9fa4efc7d6641422c3729df2d5f998ebdaa14c8f1f9f152",
	"scenery.down":                      "sha256:650b1258f522f112e9f91c1114af6affc47e4dde0470dd188b0124454446b826",
	"scenery.prune":                     "sha256:212df0b20151171c7ec70d521698c5e66146b55c17f84719d2b919cdd63060a3",
	"scenery.prune.orphaned":            "sha256:beee3ef831b18116f5cd2be34716832232eb60bf44d87e3a6d73c948629f7215",
	"scenery.dev-runtime.status":        "sha256:55fe0512941b2ce1487c3c5784fb7ff59935abe0ca0055d084d258551a655071",
	"scenery.inspect.build":             "sha256:5787ddfb761b34a352c20b45dc37f50a85356824ecb7579d00abe36a87e65572",
	"scenery.generate.plan":             "sha256:1d372fa16e37696f5e0dacc1f9af3a51c09956902cfc4ff07ce3d409ac53bea6",
	"scenery.validation.plan":           "sha256:70fcd06fd152aeb86afc4d71920d09068a85e9f9a7a5ae00b808d85942efca06",
	"scenery.validation.result":         "sha256:139558e468ee2afaf721b0f1a1b4b1fde798cd6f98748af96246c8fa6de2e0da",
	"scenery.inspect.routes":            "sha256:000c440270d6bfe7bde83bbba963e0a4614aa830b284c4fca8424e82c1ca5bd0",
	"scenery.inspect.metrics":           "sha256:6af4d264dbb1fd08f82a3b69eac6114dd400100145476bae5f8cdd2fb8f337bd",
	"scenery.inspect.traces":            "sha256:af813a0069ad6f07983cf15ba3c67300dd6952d41a4af67450ef45c3321723be",
	"scenery.inspect.ui":                "sha256:16c903f6ec1b17894cd23150bd387876af888fb957cabe010f1ba9219a24cb50",
	"scenery.assistant.status":          "sha256:feed0da108c200c5c6af881b2f32b43a1341cd84818ac020f054a84414e686db",
	"scenery.logs.query":                "sha256:56c91a3d7193c11445dfd09cfc6e98ed3fa4a9c1ac1f4f5042b3ee73d811c9b1",
	"scenery.metrics.query":             "sha256:a4583fb699388059b33c0f15cc24b547b698a8454521c27cc922ee182383dfae",
	"scenery.failure.report":            "sha256:4d4540496d4ad6467a4d1ff95b00228858e088a6da619310a057ab6ffbd9e273",
	"scenery.telemetry":                 "sha256:0eb4f93d93cc4c104cc525d2192be376eddbd336160b93a6532daa15c28988f0",
	"scenery.telemetry.report":          "sha256:802120a0613f56ec27029cda05a305b70a46aa1d39535de57a7f00695daea504",
	"scenery.inspect.harness":           "sha256:f85ff889bd47c12fef97c8f922a235989ad736775207a7cf6c2e24a5d48e4897",
	"scenery.harness.artifact":          "sha256:5fdbd3fbabd171b9226331c8d821c2a59744e7682943593896c332b8ac69eaa8",
	"scenery.harness.changed_area":      "sha256:a7240232c8378e835cac0ec4fa6f9a9236b5988ec7692d1988d43edbf5a73b0f",
	"scenery.harness.drift":             "sha256:2218980db0b2af538f8773e3a3412f1a15c2d0c66ef6574bd8910805b8eebf71",
	"scenery.harness.fixture_matrix":    "sha256:0317781d4d92e84a88e03e368cc06c8371a325ac37d48ca8982dbb70eeefb729",
	"scenery.harness.result":            "sha256:19f4764781db12ff44ec727ecfff77b0a633a238ad4b316323c862288f54afc6",
	"scenery.harness.schema_validation": "sha256:0b8037ff35c8ad553fa023abf7886d3fe7e28649400fa8d69d3a7994062d91bd",
	"scenery.harness.self":              "sha256:2560d2d766de7573a42afad53db3d1c795a9eec1a064488b8ff12b5f4395248d",
	"scenery.harness.self.summary":      "sha256:56b62406d978106c303b79a830a4655c69a0f9a8fe4d2ab74a5922b917241b7d",
	"scenery.harness.test_timing":       "sha256:ad1fdd2df41c8f116886f3d399729816183e8424d870be6dd62e2d269d53aab3",
	"scenery.harness.toolchain":         "sha256:edb5bd2a880e5ed8d87baa7906437520766479ffe7f9a26d754ce35c3656e3a3",
	"scenery.build.result":              "sha256:e56b4787ccf708ba7ca8a0172dfb8a2868e11b3b70a187bb298a043e2f171f1e",
	"scenery.build.desktop":             "sha256:7cfea8f3c11ee0afce6b9718565506a3ef86ae2de7da0265f896c67ca3eccfe7",
	"scenery.run.event":                 "sha256:783d6c49d9a0f2b57acebc0d1c11f9e155a7b2d9a3c70bc171f4a7d48e6d8138",
	"scenery.dev-runtime.wait":          "sha256:a5cc428e7d4d58de1f60eaba29ba6f572ee99edf46ef6ce70842e227eeda6d4a",
	"scenery.dev.event":                 "sha256:88d7bd9bd7e38e93de754e8508bde933e9d49d48a5391c29686e11737bb5ba2e",
	"scenery.dev.detach":                "sha256:3ba4e45cfd425a451b8a6caf3d7217014fc9d48055d00c9a158a500a255f1742",
	"scenery.edge.trust":                "sha256:5b028667b235c96636e9f3dd51f311600201534cd3bfe8d47afdf084c233eabe",
	"scenery.edge.status":               "sha256:fb04d0f874cb7d7eaa301abe534e20e145a71ba719b40d3d3f5bfdd157506ddb",
	"scenery.storage.inspect":           "sha256:f7fba39ebea700be26dfc1034cfe23c2632f1d04984f18f5e8275ae122c7a21b",
	"scenery.storage.cleanup":           "sha256:55ecaf5a78419799f6ebdb333a0a93c1e905cc1b2fdf680fa5f57e922b408708",
	"scenery.storage.delete":            "sha256:61adce71246182fb13758dc171680a622b8e8b6b7b6b7e4d30575d3fe7a40dfd",
	"scenery.storage.list":              "sha256:747dd49ea880f86361531925b5f896414ee99753c1c12d40b836f4fdb4f5f4d8",
	"scenery.storage.object":            "sha256:7b11f31427314372866fb634d42e96ce92630096b61eb2eff4fa3f79c91fc5a9",
	"scenery.snapshot.save":             "sha256:103a4cc24cc3fbeb91079f0b438e2c001d43f6d4fd07b0dbd0e0fd4ec38bdedf",
	"scenery.snapshot.verify":           "sha256:a76080eca49ac22abda65554d42ec41cf14840a412e1d2f5864699f8982dfc02",
	"scenery.snapshot.load":             "sha256:37f665657dc93df8f00a36e6e1237fb2ae08be6844a287964cf775bbca176216",
	"scenery.task.inspect":              "sha256:3adcce0faa04b452008200ea56d5c10642db57cd4980ef807d2e2b459e8495fc",
	"scenery.task.list":                 "sha256:7cbb5e4355f533e353de7e3171a2b3cb4b60c30675d884ca52733295f89a4c99",
	"scenery.durable.jobs":              "sha256:e680c8870c213d3fba5b8aa35fc7b9fda71e95fc526330f529c108728519f948",
	"scenery.version":                   "sha256:7b2f75aa63a70fdeec0ef99ff740321fcd4cebcb2b45f0e177ee4884519a4fa9",
	"scenery.worktree.create":           "sha256:1b1722d0e78a288534309bcd8181fc6866bb8de8e6faef8df6bdf4e259e046cf",
	"scenery.worktree.list":             "sha256:2d42d90ddd63a94c1e0fe12a1cf04023b7ccc167534c32751dc06a2714aed237",
	"scenery.worktree.remove":           "sha256:c9a11366bd39db3ae470555a433ecb93d82459f38ef0f645cd2c0fe456f08131",
	"scenery.worktree.upgrade":          "sha256:eec3450506fc0397847d3fc172d6cd1154f8a836fba274e63ca1466b8267930b",
}

func NewPayloadIdentity(kind string) PayloadIdentity {
	revision, ok := payloadSchemaRevisions[kind]
	if !ok {
		panic("missing CLI payload schema revision for " + kind)
	}
	return PayloadIdentity{Kind: kind, SchemaRevision: revision}
}

func WithPayloadIdentity(kind string, values map[string]any) map[string]any {
	identity := NewPayloadIdentity(kind)
	values["kind"] = identity.Kind
	values["schema_revision"] = identity.SchemaRevision
	return values
}

// PayloadSchemaRevision looks up a current command-data identity without accepting unknown kinds.
func PayloadSchemaRevision(kind string) (string, bool) {
	revision, ok := payloadSchemaRevisions[kind]
	return revision, ok
}
