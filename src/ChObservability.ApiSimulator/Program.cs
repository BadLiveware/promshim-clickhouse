using System.Diagnostics;
using System.Diagnostics.Metrics;
using System.Net;
using System.Text;
using System.Text.Json;
using Bogus;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using OpenTelemetry;
using OpenTelemetry.Logs;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

var builder = WebApplication.CreateBuilder(args);

const string ServiceName = "ch-observability-api-simulator";
const string ServiceNamespace = "ch-observability";

var otlpEndpoint = builder.Configuration["OTEL_EXPORTER_OTLP_ENDPOINT"] ?? "http://localhost:4317";
var basePort = int.TryParse(builder.Configuration["API_SIM_PORT"], out var configuredPort) ? configuredPort : 8080;
var serviceVersion = typeof(Program).Assembly.GetName().Version?.ToString() ?? "0.1.0";

var resourceBuilder = ResourceBuilder.CreateDefault()
    .AddService(serviceName: ServiceName, serviceVersion: serviceVersion, serviceNamespace: ServiceNamespace)
    .AddAttributes(new Dictionary<string, object>
    {
        ["deployment.environment"] = "local",
        ["service.instance.id"] = Environment.MachineName,
        ["otel.sdk.language"] = "dotnet",
    });

var meterName = "ChObservability.ApiSimulator.Metrics";
var meter = new Meter(meterName, serviceVersion);
var loginAttempts = meter.CreateCounter<long>("sim.api.login.attempts", unit: "requests", description: "Login attempts");
var searchRequests = meter.CreateCounter<long>("sim.api.search.requests", unit: "requests", description: "Search requests");
var checkoutRequests = meter.CreateCounter<long>("sim.api.checkout.requests", unit: "requests", description: "Checkout requests");
var syncRequests = meter.CreateCounter<long>("sim.api.sync.requests", unit: "requests", description: "Sync requests");
var requestDurationMs = meter.CreateHistogram<double>("sim.api.request.duration.ms", unit: "ms", description: "API request duration");

builder.Logging.ClearProviders();
builder.Logging.AddSimpleConsole(config =>
{
    config.SingleLine = true;
    config.TimestampFormat = "HH:mm:ss ";
});

builder.Logging.AddOpenTelemetry(options =>
{
    options.SetResourceBuilder(resourceBuilder);
    options.IncludeFormattedMessage = true;
    options.IncludeScopes = true;
    options.ParseStateValues = true;
    options.AddOtlpExporter(exporterOptions =>
    {
        exporterOptions.Endpoint = new Uri(otlpEndpoint);
        exporterOptions.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.Grpc;
    });
});

builder.Services.AddOpenTelemetry()
    .WithTracing(builder =>
    {
        builder
            .SetResourceBuilder(resourceBuilder)
            .AddAspNetCoreInstrumentation(options =>
            {
                options.RecordException = true;
            })
            .AddHttpClientInstrumentation()
            .AddOtlpExporter(options =>
            {
                options.Endpoint = new Uri(otlpEndpoint);
                options.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.Grpc;
            });
    })
    .WithMetrics(metrics =>
    {
        metrics
            .SetResourceBuilder(resourceBuilder)
            .AddAspNetCoreInstrumentation()
            .AddHttpClientInstrumentation()
            .AddMeter(meterName)
            .AddOtlpExporter(options =>
            {
                options.Endpoint = new Uri(otlpEndpoint);
                options.Protocol = OpenTelemetry.Exporter.OtlpExportProtocol.Grpc;
            });
    });

builder.Services.AddHttpClient("internal-api", client =>
{
    client.BaseAddress = new Uri($"http://localhost:{basePort}");
    client.DefaultRequestVersion = HttpVersion.Version30;
    client.DefaultVersionPolicy = HttpVersionPolicy.RequestVersionOrLower;
});

builder.Services.AddSingleton(_ => new Faker());
builder.Services.AddHostedService<BackgroundTrafficGenerator>();

var app = builder.Build();

app.UseExceptionHandler(errorApp =>
{
    errorApp.Run(async context =>
    {
        context.Response.StatusCode = StatusCodes.Status500InternalServerError;
        context.Response.ContentType = "application/json";

        var error = new
        {
            error = true,
            message = "request failed",
            requestId = context.TraceIdentifier,
        };

        await context.Response.WriteAsync(JsonSerializer.Serialize(error));
    });
});

app.Use(async (context, next) =>
{
    var sw = Stopwatch.StartNew();
    try
    {
        await next();
    }
    finally
    {
        var tags = new TagList
        {
            { "http.route", context.Request.Path.Value ?? "" },
            { "http.status_code", context.Response.StatusCode },
            { "http.method", context.Request.Method },
        };

        requestDurationMs.Record(sw.Elapsed.TotalMilliseconds, tags);
    }
});

app.MapGet("/health", () => Results.Ok("ok"));

app.MapPost("/api/login", async (Faker faker, ILogger<Program> logger, HttpContext ctx) =>
{
    var user = faker.Internet.Email();
    using var span = Activity.Current?.Source.StartActivity("api.login");
    span?.SetTag("enduser.id", user);

    logger.LogInformation("Login request started user={User}", user);

    loginAttempts.Add(1, new TagList { { "flow", "login" } });

    await SimulateDelay(Random.Shared.Next(15, 60));

    if (Random.Shared.NextDouble() < 0.08)
    {
        throw new InvalidOperationException("invalid credentials");
    }

    var token = Convert.ToBase64String(Encoding.UTF8.GetBytes($"{user}:{Guid.NewGuid():N}"));

    logger.LogInformation("Login succeeded user={User}", user);

    return Results.Ok(new { user, token, success = true });
});

app.MapGet("/api/search", async (string q, Faker faker, ILogger<Program> logger, HttpContext ctx) =>
{
    searchRequests.Add(1, new TagList { { "flow", "search" } });

    using var span = Activity.Current?.Source.StartActivity("api.search");
    span?.SetTag("search.query", q);

    await SimulateDelay(Random.Shared.Next(30, 120));

    var results = Enumerable.Range(0, Random.Shared.Next(1, 6))
        .Select(_ => new
        {
            sku = faker.Commerce.Ean13(),
            title = faker.Commerce.ProductName(),
            relevance = Math.Round(Random.Shared.NextDouble(), 3),
        })
        .ToArray();

    logger.LogInformation("Search performed user={Query} count={Count}", q, results.Length);

    return Results.Ok(results);
});

app.MapPost("/api/checkout", async (Faker faker, ILogger<Program> logger, HttpContext ctx) =>
{
    checkoutRequests.Add(1, new TagList { { "flow", "checkout" } });
    using var span = Activity.Current?.Source.StartActivity("api.checkout");

    await SimulateDelay(Random.Shared.Next(50, 200));

    if (Random.Shared.NextDouble() < 0.12)
    {
        throw new InvalidOperationException("payment provider unavailable");
    }

    var sku = faker.Commerce.Ean13();
    var amount = Math.Round(faker.Finance.Amount(5, 250), 2);

    logger.LogInformation("Checkout completed sku={SKU} amount={Amount}", sku, amount);

    return Results.Ok(new { sku, amount, currency = "USD", status = "accepted" });
});

app.MapPost("/api/sync", async (Faker faker, ILogger<Program> logger, HttpContext ctx) =>
{
    syncRequests.Add(1, new TagList { { "flow", "sync" } });
    using var span = Activity.Current?.Source.StartActivity("api.sync");

    await SimulateDelay(Random.Shared.Next(20, 150));

    if (Random.Shared.NextDouble() < 0.07)
    {
        throw new IOException("upstream sync timeout");
    }

    var payload = new
    {
        batches = Random.Shared.Next(1, 8),
        records = Random.Shared.Next(20, 120),
        checksum = Guid.NewGuid().ToString("N"),
    };

    logger.LogInformation("Sync finished batches={Batches} records={Records}", payload.batches, payload.records);

    return Results.Ok(payload);
});

app.Run($"http://0.0.0.0:{basePort}");

static async Task SimulateDelay(int delayMs)
{
    await Task.Delay(delayMs);
}

internal sealed class BackgroundTrafficGenerator(
    IHttpClientFactory httpClientFactory,
    ILogger<BackgroundTrafficGenerator> logger,
    Faker faker,
    IConfiguration configuration) : BackgroundService
{
    private readonly int _delayMs = int.TryParse(configuration["API_SIM_LOOP_DELAY_MS"], out var delay) ? delay : 200;
    private readonly int _iterations = int.TryParse(configuration["API_SIM_MAX_ITERATIONS"], out var max) ? max : -1;
    private readonly bool _run = !bool.TryParse(configuration["API_SIM_DISABLE"], out var disable) || !disable;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        if (!_run)
        {
            logger.LogInformation("API background traffic generator is disabled (API_SIM_DISABLE=true)");
            return;
        }

        var client = httpClientFactory.CreateClient("internal-api");
        var loop = 0;

        var flowOptions = new[] { "/api/login", "/api/search?q=demo", "/api/checkout", "/api/sync" };

        while (!stoppingToken.IsCancellationRequested)
        {
            if (_iterations > 0 && loop >= _iterations)
            {
                logger.LogInformation("API background traffic max iterations reached: {Iterations}", _iterations);
                return;
            }

            loop++;
            var flow = flowOptions[Random.Shared.Next(flowOptions.Length)];
            var method = flow.StartsWith("/api/login") || flow.StartsWith("/api/checkout") || flow.StartsWith("/api/sync")
                ? HttpMethod.Post
                : HttpMethod.Get;

            using var request = new HttpRequestMessage(method, flow);

            if (method == HttpMethod.Post)
            {
                request.Content = new StringContent(
                    JsonSerializer.Serialize(new
                    {
                        user = faker.Internet.UserName(),
                        correlation = Guid.NewGuid().ToString("N"),
                    }),
                    Encoding.UTF8,
                    "application/json");
            }

            try
            {
                var response = await client.SendAsync(request, stoppingToken);
                if (!response.IsSuccessStatusCode)
                {
                    logger.LogWarning("Background call returned non-success for {Method} {Path}: {Status}", method, flow, response.StatusCode);
                }
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch (Exception ex)
            {
                logger.LogError(ex, "Background call failed for {Path}", flow);
            }

            try
            {
                await Task.Delay(_delayMs, stoppingToken);
            }
            catch (OperationCanceledException)
            {
                return;
            }
        }
    }
}
