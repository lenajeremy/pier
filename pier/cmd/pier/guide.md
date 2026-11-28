# Pier, for people and coding agents

Pier runs apps on your own server: it builds a folder or a GitHub push, gives
every build its own address, and keeps production on the one you choose.
Everything below works without a terminal prompt, so an agent can run it.

## Sign in

    pier login pier.example.com      # once; a person approves it in the dashboard

Or, in CI and other machines, set PIER_SERVER (the dashboard's address) and
PIER_TOKEN (a token from pier login, or pierd token on the server).

## Deploy

    pier deploy --project shop --create      # makes the project if it's new, then a preview
    pier deploy                              # the folder remembers its project in .pier/project.json
    pier deploy --prod                       # production

Each deploy prints its address. Pier builds what it finds: plain HTML, a Vite or
other framework site, a server app, or a Dockerfile. A server app listens on
$PORT, on 0.0.0.0. Build settings (root folder, commands, output folder) are on
the project's Settings tab. Output is plain when it isn't a terminal.

    pier deployments          # newest first
    pier logs --runtime --once
    pier logs --build --once
    pier rollback             # production back to the one before; --undo undoes it
    pier promote ID           # production to a ready deployment

## Variables

    pier env set API_KEY=sk_123 STRIPE_KEY=... [--env production|preview]
    pier env                  # names and where they reach
    pier env rm API_KEY

A deploy gets variables when it builds; redeploy after a change.

## Postgres

    pier db create            # the project's server: "production" for production,
                              # "preview" for previews, both given as DATABASE_URL
    pier db add analytics --env production --var ANALYTICS_URL
    pier db link preview --env none          # what a database serves
    pier db                   # the server and its databases

Apps read DATABASE_URL (or the variable you chose); it reaches the database only
from the project's own apps. From your computer:

    pier db tunnel --env .env.local          # localhost:5433, and its URL in .env.local
    pier db psql [--db NAME]
    pier db external [--db NAME]             # one URL for a service outside Pier, over TLS

Node: new pg.Pool({ connectionString: process.env.DATABASE_URL })
Python: psycopg.connect(os.environ["DATABASE_URL"])
Prisma, Drizzle and the rest read DATABASE_URL as they are.

## File storage

Buckets live on the owner's Cloudflare R2, connected once in the dashboard's
Integrations. Then:

    pier storage add files                   # private; serves production and previews
    pier storage add media --public --env production
    pier storage                             # buckets, what they serve, their files
    pier storage env files                   # its variables, for a local .env

A deploy gets each bucket linked to it as <PREFIX>_BUCKET, _ENDPOINT,
_ACCESS_KEY_ID, _SECRET_ACCESS_KEY, _REGION and, if public, _PUBLIC_URL. The
first bucket's prefix is STORAGE; others take their name (MEDIA_...). The
STORAGE bucket also sets AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
AWS_ENDPOINT_URL_S3 and AWS_REGION, so S3 clients work with no setup. Its key
reaches that bucket and no other.

Node (@aws-sdk/client-s3):

    const s3 = new S3Client({
      region: process.env.STORAGE_REGION,
      endpoint: process.env.STORAGE_ENDPOINT,
      credentials: {
        accessKeyId: process.env.STORAGE_ACCESS_KEY_ID,
        secretAccessKey: process.env.STORAGE_SECRET_ACCESS_KEY,
      },
    })
    await s3.send(new PutObjectCommand({ Bucket: process.env.STORAGE_BUCKET, Key: "invoices/1.pdf", Body: data }))

Python (boto3):

    s3 = boto3.client("s3", endpoint_url=os.environ["STORAGE_ENDPOINT"], region_name="auto",
        aws_access_key_id=os.environ["STORAGE_ACCESS_KEY_ID"],
        aws_secret_access_key=os.environ["STORAGE_SECRET_ACCESS_KEY"])

A public bucket's file is at <PREFIX>_PUBLIC_URL + "/" + key, served by
Cloudflare. Private files go to the browser through presigned links your app
makes. From your computer:

    pier storage put ./logo.png media/brand/
    pier storage ls media/brand
    pier storage get files/invoices/1.pdf

## Jobs

    pier jobs                 # scheduled jobs and their last runs
    pier jobs run NAME        # run one now and print its output

## Deleting

Deletes ask for the name in a terminal; outside one they need --yes:

    pier storage rm files --yes              # every file in it goes too
    pier db rm analytics --yes               # a last backup is kept
