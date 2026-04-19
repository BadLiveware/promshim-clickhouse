using System.Diagnostics;
using System.Diagnostics.Metrics;
using Bogus;
using Microsoft.Extensions.Logging;
using OpenTelemetry;
using OpenTelemetry.Logs;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

const string ServiceName = "ch-observability-simulator";
const string ServiceNamespace = "ch-observability";
const string ActivitySourceName = "ChObservability.Simulator.Activities";
const string MeterName = "ChObservability.Simulator.Metrics";

var serviceVersion = typeof(Program).Assembly.GetName().Version?.ToString() ?? "0.1.0";
var otlpEndpoint = Environment.GetEnvironmentVariable("OTEL_EXPORTER_OTLP_ENDPOINT") ?? "http://localhost:4317";
var loopDelayMs = int.TryParse(Environment.GetEnvironmentVariable("SIM_LOOP_DELAY_MS"), out var configuredDelay) ? configuredDelay : 250;
var maxIterations = int.TryParse(Environment.GetEnvironmentVariable("SIM_MAX_ITERATIONS"), out var configuredMaxIterations)
    ? configuredMaxIterations
    : -1;

var resourceBuilder = ResourceBuilder.CreateDefault()
    .AddService(serviceName: ServiceName, serviceVersion: serviceVersion, serviceNamespace: ServiceNamespace)
    .AddAttributes(new Dictionary<string, object>
    {
        ["deployment.environment"] = "local",
        ["service.instance.id"] = Environment.MachineName,
    });

using var activitySource = new ActivitySource(ActivitySourceName);
using var meter = new Meter(MeterName, serviceVersion);

var flowsTotal = meter.CreateCounter<long>("sim.flows.total", unit: "flows", description: "Number of simulated flows");
var flowsFailed = meter.CreateCounter<long>("sim.flows.failed", unit: "flows", description: "Number of failed simulated flows");
var flowDurationMs = meter.CreateHistogram<double>("sim.flow.duration.ms", unit: "ms", description: "Flow duration in milliseconds");
var purchaseAmount = meter.CreateHistogram<double>("sim.purchase.amount", unit: "USD", description: "Synthetic purchase amount");
var activeUsers = meter.CreateUpDownCounter<long>("sim.active.users", unit: "users", description: "Current number of active synthetic users");

using var tracerProvider = Sdk.CreateTracerProviderBuilder()
    .SetResourceBuilder(resourceBuilder)
    .AddSource(ActivitySourceName)
    .AddOtlpExporter(options =>
    {
        options.Endpoint = new Uri(otlpEndpoint);
        options.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.Grpc;
    })
    .Build();

using var meterProvider = Sdk.CreateMeterProviderBuilder()
    .SetResourceBuilder(resourceBuilder)
    .AddMeter(MeterName)
    .AddOtlpExporter(options =>
    {
        options.Endpoint = new Uri(otlpEndpoint);
        options.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.Grpc;
    })
    .Build();

using var loggerFactory = LoggerFactory.Create(logging =>
{
    logging.SetMinimumLevel(LogLevel.Information);
    logging.AddOpenTelemetry(options =>
    {
        options.SetResourceBuilder(resourceBuilder);
        options.IncludeScopes = true;
        options.IncludeFormattedMessage = true;
        options.ParseStateValues = true;
        options.AddOtlpExporter(exporterOptions =>
        {
            exporterOptions.Endpoint = new Uri(otlpEndpoint);
            exporterOptions.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.Grpc;
        });
    });
});

var logger = loggerFactory.CreateLogger("simulator");
var faker = new Faker();

using var cts = new CancellationTokenSource();
Console.CancelKeyPress += (_, eventArgs) =>
{
    eventArgs.Cancel = true;
    cts.Cancel();
};

logger.LogInformation(
    "Starting simulator. OTLP endpoint={OtlpEndpoint}, loopDelayMs={LoopDelayMs}, maxIterations={MaxIterations}",
    otlpEndpoint,
    loopDelayMs,
    maxIterations);

var iteration = 0;
while (!cts.IsCancellationRequested)
{
    if (maxIterations > 0 && iteration >= maxIterations)
    {
        break;
    }

    var syntheticUser = new SyntheticUser(
        UserId: faker.Random.Guid().ToString("N"),
        Email: faker.Internet.Email(),
        Country: faker.Address.CountryCode(),
        Plan: faker.PickRandom("free", "pro", "enterprise"));

    var flowType = faker.PickRandom("browse", "search", "checkout", "api_sync");
    var tags = new TagList
    {
        { "flow.type", flowType },
        { "user.plan", syntheticUser.Plan },
        { "user.country", syntheticUser.Country },
    };

    var stopwatch = Stopwatch.StartNew();
    flowsTotal.Add(1, tags);
    activeUsers.Add(1, tags);

    try
    {
        await RunFlowAsync(activitySource, logger, faker, syntheticUser, flowType, purchaseAmount, cts.Token);
        flowDurationMs.Record(stopwatch.Elapsed.TotalMilliseconds, tags);
    }
    catch (Exception ex)
    {
        flowsFailed.Add(1, tags);
        flowDurationMs.Record(stopwatch.Elapsed.TotalMilliseconds, tags);
        logger.LogError(ex, "Flow failed for userId={UserId} flowType={FlowType}", syntheticUser.UserId, flowType);
    }
    finally
    {
        activeUsers.Add(-1, tags);
    }

    iteration++;

    try
    {
        await Task.Delay(loopDelayMs, cts.Token);
    }
    catch (OperationCanceledException)
    {
        break;
    }
}

logger.LogInformation("Simulator stopped.");

static async Task RunFlowAsync(
    ActivitySource activitySource,
    ILogger logger,
    Faker faker,
    SyntheticUser user,
    string flowType,
    Histogram<double> purchaseAmount,
    CancellationToken cancellationToken)
{
    using var flowActivity = activitySource.StartActivity("user.flow", ActivityKind.Server);
    flowActivity?.SetTag("flow.type", flowType);
    flowActivity?.SetTag("enduser.id", user.UserId);
    flowActivity?.SetTag("enduser.email", user.Email);
    flowActivity?.SetTag("enduser.plan", user.Plan);
    flowActivity?.SetTag("enduser.country", user.Country);

    using var scope = logger.BeginScope(new Dictionary<string, object>
    {
        ["user.id"] = user.UserId,
        ["flow.type"] = flowType,
        ["user.plan"] = user.Plan,
        ["user.country"] = user.Country,
    });

    logger.LogInformation("Flow started");

    await SimulateStepAsync(activitySource, "auth.validate_token", 10, 40, cancellationToken);
    await SimulateStepAsync(activitySource, "feature_flags.resolve", 5, 30, cancellationToken);

    if (flowType is "browse" or "search")
    {
        await SimulateStepAsync(activitySource, "catalog.query", 20, 100, cancellationToken);
        await SimulateStepAsync(activitySource, "recommendations.fetch", 20, 120, cancellationToken);
    }
    else if (flowType == "checkout")
    {
        await SimulateStepAsync(activitySource, "cart.load", 15, 80, cancellationToken);
        await SimulateStepAsync(activitySource, "inventory.reserve", 20, 100, cancellationToken);
        await SimulateStepAsync(activitySource, "payment.authorize", 40, 200, cancellationToken, errorRate: 0.15);
        await SimulateStepAsync(activitySource, "order.persist", 20, 90, cancellationToken, errorRate: 0.05);

        var amount = faker.Finance.Amount(10, 500);
        purchaseAmount.Record((double)amount, new TagList
        {
            { "currency", "USD" },
            { "user.plan", user.Plan },
        });

        logger.LogInformation("Checkout succeeded amount={Amount}", amount);
    }
    else
    {
        await SimulateStepAsync(activitySource, "sync.pull_remote", 30, 150, cancellationToken);
        await SimulateStepAsync(activitySource, "sync.merge", 20, 100, cancellationToken, errorRate: 0.1);
        await SimulateStepAsync(activitySource, "sync.push_results", 20, 100, cancellationToken);
    }

    logger.LogInformation("Flow completed");
}

static async Task SimulateStepAsync(
    ActivitySource source,
    string stepName,
    int minLatencyMs,
    int maxLatencyMs,
    CancellationToken cancellationToken,
    double errorRate = 0.0)
{
    using var step = source.StartActivity(stepName, ActivityKind.Internal);
    var latency = Random.Shared.Next(minLatencyMs, maxLatencyMs + 1);

    step?.SetTag("step.latency.ms", latency);

    await Task.Delay(latency, cancellationToken);

    if (errorRate > 0 && Random.Shared.NextDouble() < errorRate)
    {
        step?.SetStatus(ActivityStatusCode.Error, "simulated failure");
        throw new InvalidOperationException($"Simulated failure in step '{stepName}'");
    }

    step?.SetStatus(ActivityStatusCode.Ok);
}

internal sealed record SyntheticUser(string UserId, string Email, string Country, string Plan);
