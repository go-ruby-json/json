# frozen_string_literal: true
#
# Pure-Ruby usage of the JSON module, as provided by go-embedded-ruby (rbgo).
# Run it with:  rbgo examples/json_usage.rb

require "json"

doc = {
  "domain"   => "example.gouv.fr",
  "features" => ["mailbox", "webmail"],
  "active"   => true,
}

# Serialise.
json = JSON.generate(doc)
puts json

# Parse back.
back = JSON.parse(json)
puts back["features"].join(", ")
puts back["active"]

# Human-readable output.
puts JSON.pretty_generate(doc)
