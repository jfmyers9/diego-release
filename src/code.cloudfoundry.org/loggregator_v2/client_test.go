package loggregator_v2_test

import (
	"time"

	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/loggregator_v2"
	lfake "github.com/cloudfoundry/dropsonde/log_sender/fake"
	mfake "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	"github.com/cloudfoundry/dropsonde/logs"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type testHandler struct{}

var _ = Describe("Client", func() {
	var (
		config    loggregator_v2.MetronConfig
		logger    *lagertest.TestLogger
		client    loggregator_v2.Client
		clientErr error
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("loggregator-client")
	})

	JustBeforeEach(func() {
		client, clientErr = loggregator_v2.NewClient(logger, config)
	})

	Context("when v2 api is disabled", func() {
		var (
			logSender    *lfake.FakeLogSender
			metricSender *mfake.FakeMetricSender
		)

		BeforeEach(func() {
			logSender = &lfake.FakeLogSender{}
			metricSender = mfake.NewFakeMetricSender()
			config.UseV2API = false
			logs.Initialize(logSender)
			metrics.Initialize(metricSender, nil)
		})

		It("sends app logs", func() {
			client.SendAppLog("app-id", "message", "source-type", "source-instance")
			Expect(logSender.GetLogs()).To(ConsistOf(lfake.Log{AppId: "app-id", Message: "message",
				SourceType: "source-type", SourceInstance: "source-instance", MessageType: "OUT"}))
		})

		It("sends app error logs", func() {
			client.SendAppErrorLog("app-id", "message", "source-type", "source-instance")
			Expect(logSender.GetLogs()).To(ConsistOf(lfake.Log{AppId: "app-id", Message: "message",
				SourceType: "source-type", SourceInstance: "source-instance", MessageType: "ERR"}))
		})

		It("sends app metrics", func() {
			metric := events.ContainerMetric{
				ApplicationId: proto.String("app-id"),
			}
			client.SendAppMetrics(&metric)
			Expect(metricSender.Events()).To(ConsistOf(&metric))
		})

		It("sends component duration", func() {
			client.SendDuration("test-name", 1*time.Nanosecond)
			Expect(metricSender.HasValue("test-name")).To(BeTrue())
			Expect(metricSender.GetValue("test-name")).To(Equal(mfake.Metric{Value: 1, Unit: "nanos"}))
		})

		It("sends component data in MebiBytes", func() {
			client.SendMebiBytes("test-name", 100)
			Expect(metricSender.HasValue("test-name")).To(BeTrue())
			Expect(metricSender.GetValue("test-name")).To(Equal(mfake.Metric{Value: 100, Unit: "MiB"}))
		})

		It("sends component metric", func() {
			client.SendMetric("test-name", 1)
			Expect(metricSender.HasValue("test-name")).To(BeTrue())
			Expect(metricSender.GetValue("test-name")).To(Equal(mfake.Metric{Value: 1, Unit: "Metric"}))
		})

		It("sends component bytes/sec", func() {
			client.SendBytesPerSecond("test-name", 100.1)
			Expect(metricSender.HasValue("test-name")).To(BeTrue())
			Expect(metricSender.GetValue("test-name")).To(Equal(mfake.Metric{Value: 100.1, Unit: "B/s"}))
		})

		It("sends component req/sec", func() {
			client.SendRequestsPerSecond("test-name", 100.1)
			Expect(metricSender.HasValue("test-name")).To(BeTrue())
			Expect(metricSender.GetValue("test-name")).To(Equal(mfake.Metric{Value: 100.1, Unit: "Req/s"}))
		})
	})

	Context("when v2 api is enabled", func() {
		var (
			receivers   chan loggregator_v2.Ingress_SenderServer
			grpcRunner  *GrpcRunner
			grpcProcess ifrit.Process
		)

		BeforeEach(func() {
			var err error
			grpcRunner, err = NewGRPCRunner("fixtures/metron.crt", "fixtures/metron.key", "fixtures/CA.crt")
			Expect(err).NotTo(HaveOccurred())
			grpcProcess = ginkgomon.Invoke(grpcRunner)

			config = loggregator_v2.MetronConfig{
				UseV2API:      true,
				APIPort:       grpcRunner.Port(),
				JobDeployment: "cf-warden-diego",
				JobName:       "rep",
				JobIndex:      "0",
				JobIP:         "10.244.34.6",
				JobOrigin:     "test-origin",
			}
			receivers = grpcRunner.Receivers()
		})

		AfterEach(func() {
			ginkgomon.Interrupt(grpcProcess)
		})

		Context("the cert or key path are invalid", func() {
			BeforeEach(func() {
				config.CertPath = "/some/invalid/path"
			})

			It("returns an error", func() {
				Expect(clientErr).To(HaveOccurred(), "client didn't return an error")
			})
		})

		Context("the ca cert path is invalid", func() {
			BeforeEach(func() {
				config.CACertPath = "/some/invalid/path"
			})

			It("returns an error", func() {
				Expect(clientErr).To(HaveOccurred(), "client didn't return an error")
			})
		})

		Context("the ca cert is invalid", func() {
			BeforeEach(func() {
				config.CACertPath = "fixtures/invalid-ca.crt"
			})

			It("returns an error", func() {
				Expect(clientErr).To(HaveOccurred(), "client didn't return an error")
			})
		})

		Context("cannot connect to the server", func() {
			BeforeEach(func() {
				config.CACertPath = "fixtures/CA.crt"
				config.CertPath = "fixtures/client.crt"
				config.KeyPath = "fixtures/client.key"
				config.APIPort = 1234
			})

			JustBeforeEach(func() {
				Expect(clientErr).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				Expect(client.SendAppLog("app-id", "message", "source-type", "source-instance")).NotTo(Succeed())
			})
		})

		Context("when valid configuration is used", func() {
			BeforeEach(func() {
				config.CACertPath = "fixtures/CA.crt"
				config.CertPath = "fixtures/client.crt"
				config.KeyPath = "fixtures/client.key"
			})

			JustBeforeEach(func() {
				Expect(clientErr).NotTo(HaveOccurred())
				Expect(client).NotTo(BeNil())
			})

			It("sends app logs", func() {
				Consistently(func() error {
					return client.SendAppLog("app-id", "message", "source-type", "source-instance")
				}).Should(Succeed())
				var recv loggregator_v2.Ingress_SenderServer
				Eventually(receivers).Should(Receive(&recv))
				env, err := recv.Recv()
				Expect(err).NotTo(HaveOccurred())

				Expect(env.Tags["deployment"].GetText()).To(Equal("cf-warden-diego"))
				Expect(env.Tags["job"].GetText()).To(Equal("rep"))
				Expect(env.Tags["index"].GetText()).To(Equal("0"))
				Expect(env.Tags["ip"].GetText()).To(Equal("10.244.34.6"))
				Expect(env.Tags["origin"].GetText()).To(Equal("test-origin"))
				Expect(env.Tags["source_instance"].GetText()).To(Equal("source-instance"))
				Expect(env.SourceId).To(Equal("app-id"))
				Expect(env.InstanceId).To(Equal("source-instance"))

				ts := time.Unix(0, env.GetTimestamp())
				Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
				log := env.GetLog()
				Expect(log).NotTo(BeNil())
				Expect(log.GetPayload()).To(Equal([]byte("message")))
				Expect(log.Type).To(Equal(loggregator_v2.OUT))
			})

			It("sends app error logs", func() {
				Consistently(func() error {
					return client.SendAppErrorLog("app-id", "message", "source-type", "source-instance")
				}).Should(Succeed())
				var recv loggregator_v2.Ingress_SenderServer
				Eventually(receivers).Should(Receive(&recv))
				env, err := recv.Recv()
				Expect(err).NotTo(HaveOccurred())

				Expect(env.Tags["deployment"].GetText()).To(Equal("cf-warden-diego"))
				Expect(env.Tags["job"].GetText()).To(Equal("rep"))
				Expect(env.Tags["index"].GetText()).To(Equal("0"))
				Expect(env.Tags["ip"].GetText()).To(Equal("10.244.34.6"))
				Expect(env.Tags["origin"].GetText()).To(Equal("test-origin"))
				Expect(env.Tags["source_instance"].GetText()).To(Equal("source-instance"))
				Expect(env.SourceId).To(Equal("app-id"))
				Expect(env.InstanceId).To(Equal("source-instance"))

				ts := time.Unix(0, env.GetTimestamp())
				Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
				log := env.GetLog()
				Expect(log).NotTo(BeNil())
				Expect(log.GetPayload()).To(Equal([]byte("message")))
				Expect(log.Type).To(Equal(loggregator_v2.ERR))
			})

			It("sends app metrics", func() {
				metric := events.ContainerMetric{
					ApplicationId:    proto.String("app-id"),
					CpuPercentage:    proto.Float64(10.0),
					MemoryBytes:      proto.Uint64(10),
					DiskBytes:        proto.Uint64(10),
					MemoryBytesQuota: proto.Uint64(20),
					DiskBytesQuota:   proto.Uint64(20),
					InstanceIndex:    proto.Int32(5),
				}
				Consistently(func() error {
					return client.SendAppMetrics(&metric)
				}).Should(Succeed())
				var recv loggregator_v2.Ingress_SenderServer
				Eventually(receivers).Should(Receive(&recv))
				env, err := recv.Recv()
				Expect(err).NotTo(HaveOccurred())

				Expect(env.Tags["deployment"].GetText()).To(Equal("cf-warden-diego"))
				Expect(env.Tags["job"].GetText()).To(Equal("rep"))
				Expect(env.Tags["index"].GetText()).To(Equal("0"))
				Expect(env.Tags["ip"].GetText()).To(Equal("10.244.34.6"))
				Expect(env.Tags["origin"].GetText()).To(Equal("test-origin"))

				ts := time.Unix(0, env.GetTimestamp())
				Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
				metrics := env.GetGauge()
				Expect(metrics).NotTo(BeNil())
				Expect(env.GetSourceId()).To(Equal("app-id"))
				Expect(metrics.GetMetrics()).To(HaveLen(6))
				Expect(metrics.GetMetrics()["instance_index"].GetValue()).To(Equal(5.0))
				Expect(metrics.GetMetrics()["cpu"].GetValue()).To(Equal(10.0))
				Expect(metrics.GetMetrics()["memory"].GetValue()).To(Equal(10.0))
				Expect(metrics.GetMetrics()["disk"].GetValue()).To(Equal(10.0))
				Expect(metrics.GetMetrics()["memory_quota"].GetValue()).To(Equal(20.0))
				Expect(metrics.GetMetrics()["disk_quota"].GetValue()).To(Equal(20.0))
			})

			Context("when component metrics are emitted", func() {
				It("sends duration info", func() {
					Consistently(func() error {
						return client.SendDuration("test-name", 1*time.Nanosecond)
					}).Should(Succeed())
					var recv loggregator_v2.Ingress_SenderServer
					Eventually(receivers).Should(Receive(&recv))
					env, err := recv.Recv()
					Expect(err).NotTo(HaveOccurred())

					ts := time.Unix(0, env.GetTimestamp())
					Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
					message := env.GetGauge()
					Expect(message).NotTo(BeNil())
					Expect(message.GetMetrics()["test-name"].GetValue()).To(Equal(float64(1)))
					Expect(message.GetMetrics()["test-name"].GetUnit()).To(Equal("nanos"))
				})

				It("sends mebibytes info", func() {
					Consistently(func() error {
						return client.SendMebiBytes("test-name", 10)
					}).Should(Succeed())
					var recv loggregator_v2.Ingress_SenderServer
					Eventually(receivers).Should(Receive(&recv))
					env, err := recv.Recv()
					Expect(err).NotTo(HaveOccurred())

					ts := time.Unix(0, env.GetTimestamp())
					Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
					message := env.GetGauge()
					Expect(message).NotTo(BeNil())
					Expect(message.GetMetrics()["test-name"].GetValue()).To(Equal(float64(10)))
					Expect(message.GetMetrics()["test-name"].GetUnit()).To(Equal("MiB"))
				})

				It("sends metrics info", func() {
					Consistently(func() error {
						return client.SendMetric("test-name", 11)
					}).Should(Succeed())
					var recv loggregator_v2.Ingress_SenderServer
					Eventually(receivers).Should(Receive(&recv))
					env, err := recv.Recv()
					Expect(err).NotTo(HaveOccurred())

					ts := time.Unix(0, env.GetTimestamp())
					Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
					message := env.GetGauge()
					Expect(message).NotTo(BeNil())
					Expect(message.GetMetrics()["test-name"].GetValue()).To(Equal(float64(11)))
					Expect(message.GetMetrics()["test-name"].GetUnit()).To(Equal("Metric"))
				})

				It("sends requests per second info", func() {
					Consistently(func() error {
						return client.SendRequestsPerSecond("test-name", 11)
					}).Should(Succeed())
					var recv loggregator_v2.Ingress_SenderServer
					Eventually(receivers).Should(Receive(&recv))
					env, err := recv.Recv()
					Expect(err).NotTo(HaveOccurred())

					ts := time.Unix(0, env.GetTimestamp())
					Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
					message := env.GetGauge()
					Expect(message).NotTo(BeNil())
					Expect(message.GetMetrics()["test-name"].GetValue()).To(Equal(float64(11)))
				})

				It("sends bytes per second info", func() {
					Consistently(func() error {
						return client.SendBytesPerSecond("test-name", 10)
					}).Should(Succeed())
					var recv loggregator_v2.Ingress_SenderServer
					Eventually(receivers).Should(Receive(&recv))
					env, err := recv.Recv()
					Expect(err).NotTo(HaveOccurred())

					ts := time.Unix(0, env.GetTimestamp())
					Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
					message := env.GetGauge()
					Expect(message).NotTo(BeNil())
					Expect(message.GetMetrics()["test-name"].GetValue()).To(Equal(float64(10)))
					Expect(message.GetMetrics()["test-name"].GetUnit()).To(Equal("B/s"))
				})

				It("increments counter", func() {
					Consistently(func() error {
						return client.IncrementCounter("test-name")
					}).Should(Succeed())
					var recv loggregator_v2.Ingress_SenderServer
					Eventually(receivers).Should(Receive(&recv))
					env, err := recv.Recv()
					Expect(err).NotTo(HaveOccurred())

					ts := time.Unix(0, env.GetTimestamp())
					Expect(ts).Should(BeTemporally("~", time.Now(), time.Second))
					message := env.GetCounter()
					Expect(message).NotTo(BeNil())
					Expect(message.GetName()).To(Equal("test-name"))
					Expect(message.GetDelta()).To(Equal(uint64(1)))
				})
			})

			Context("when the server goes away and comes back", func() {
				JustBeforeEach(func() {
					Expect(client.SendAppErrorLog("app-id", "message", "source-type", "source-instance")).To(Succeed())
					ginkgomon.Interrupt(grpcProcess)

					// wait for the client to detect the error
					Eventually(func() error {
						return client.SendAppErrorLog("app-id", "message", "source-type", "source-instance")
					}).ShouldNot(Succeed())
					grpcProcess = ginkgomon.Invoke(grpcRunner)

					// make sure the server stays up
					Consistently(grpcProcess.Wait()).ShouldNot(Receive())
				})

				It("should reconnect", func() {
					Eventually(func() error {
						return client.SendAppErrorLog("app-id", "message", "source-type", "source-instance")
					}).Should(Succeed())
				})
			})
		})
	})
})
