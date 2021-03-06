Sharding is a method of horizontally partitioning a database to store
data across two or more database servers. This document explains how
sharding works in Vitess and the types of sharding that Vitess supports.

## Overview

In Vitess, a shard is a partition of a keyspace. In turn, the keyspace
might be a partition of the whole database. For example, a database might
have one keyspace for product data and another for user data. The shard
contains a subset of records within its keyspace.

For example, if an application's "user" keyspace is split into two
shards, each shard contains records for approximately half of the
application's users. Similarly, each user's information is stored
in only one shard.

A Vitess shard typically contains one MySQL master and many MySQL
slaves. The master handles write operations, while slaves handle
read-only traffic, batch processing operations, and other tasks.
Each MySQL instance within the shard should have the same data,
excepting some replication lag.

### Supported Operations

Vitess supports the following types of sharding operations:

* **Horizontal sharding:** Splitting or merging shards in a sharded keyspace
* **Vertical sharding:** Moving tables from an unsharded keyspace to
    a different keyspace.

With these features, you can start with a single keyspace that contains
all of your data (in multiple tables). As your database grows, you can
move tables to different keyspaces and shard some or all of those keyspaces
without any real downtime for your application.

## Range-based Sharding

Vitess uses range-based sharding to manage data across multiple shards.
(Vitess can also support a [custom sharding](#custom-sharding) scheme.)

In range-based sharding, each record in a keyspace is associated with
a sharding key that is stored with the record. The sharding key value
is also the primary key for sharded data. Records with the same sharding
key are always collocated on the same shard.

**Note:** The API uses the term "keyspace ID" to refer to the sharding key.

The full set of shards covers the range of possible sharding key values.
To guarantee a balanced use of shards, the sharding scheme should
ensure an even distribution of sharding keys across the keyspace's
shards. That distribution makes it easier to reshard the keyspace
at a later time using a more granular division of sharding keys.

Vitess calculates the sharding key or keys for each query and then
routes that query to the appropriate shards. For example, a query
that updates information about a particular user might be directed to
a single shard in the application's "user" keyspace. On the other hand,
a query that retrieves information about several products might be
directed to one or more shards in the application's "product" keyspace.

### Sharding Keys

As discussed above, Vitess calculates the sharding keys associated
with any particular query and then routes the query to the appropriate
shards.

Vitess supports two types of sharding keys:

* **Binary data:** The key is an array of bytes. Vitess uses regular
    byte-array comparison to determine which shard should handle the
    query. The MySQL representation for this type of sharding key is
    a <code>VARBINARY</code> field.

* **64-bit unsigned integer:** Vitess converts the 64-bit integer into
    a byte array by copying the bytes, most significant byte first,
    into 8 bytes. Vitess then uses byte-array comparison to identify the
    right shards to handle the query. The MySQL representation for this
    type of sharding key is a <code>bigint(20) UNSIGNED</code> field.

A sharded keyspace contains information about the type of sharding key
that the keyspace uses. Each database table in the shard has a column
that stores the sharding key associated with each row in the table. The
sharding key column in each table has the same name and column type.

A common example of a sharding key is the 64-bit hash of a user ID. The
hashing function ensures that the sharding keys are evenly distributed
in the space.

**Note:** If the vtgate v3 API is used, the sharding key value is no longer materialized. Instead, vtgate can calculate it on the fly when reading and inserting data. (A valid VSchema is required to tell vtgate how to calculate the sharding key value.) 

### Key Ranges and Partitions

Vitess uses key ranges to determine which shards should handle any
particular query.

* A **key range** is a series of consecutive sharding key values. It
    has starting and ending values. A key falls inside the range if
    it is equal to or greater than the start value and strictly less
    than the end value.
* A **partition** represents a set of key ranges that covers the entire
    space.

When building the serving graph for a keyspace that uses range-based
sharding, Vitess ensures that each shard is valid and that the shards
collectively constitute a full partition. In each keyspace, one shard
must have a key range with an empty start value and one shard, which
could be the same shard, must have a key range with an empty end value.

* An empty start value represents the lowest value, and all values are
    greater than it.
* An empty end value represents a value larger than the highest possible
    value, and all values are strictly lower than it.

Vitess always converts sharding keys to byte arrays before routing
queries. The value [ 0x80 ] is the middle value for sharding keys.
So, in a keyspace with two shards, sharding keys that have a byte-array
value lower than 0x80 are assigned to one shard. Keys with a byte-array
value equal to or higher than 0x80 are assigned to the other shard.

Several sample key ranges are shown below:

``` sh
Start=[], End=[]: Full Key Range
Start=[], End=[0x80]: Lower half of the Key Range.
Start=[0x80], End=[]: Upper half of the Key Range.
Start=[0x40], End=[0x80]: Second quarter of the Key Range.
```

Two key ranges are consecutive if the end value of one range equals the
start value of the other range.

### Shard Names in Range-Based Keyspaces

In range-based, sharded keyspaces, a shard's name identifies the start
and end of the shard's key range, printed in hexadecimal and separated
by a hyphen. For instance, if a shard's key range is the array of bytes
beginning with [ 0x80 ] and ending, noninclusively, with [ 0xc0], then
the shard's name is <code>80-c0</code>.

Using this naming convention, the following four shards would be a valid
full partition:

* -40
* 40-80
* 80-c0
* c0-

## Resharding

In Vitess, resharding describes the process of updating the sharding
scheme for a keyspace and dynamically reorganizing data to match the
new scheme. During resharding, Vitess copies, verifies, and keeps
data up-to-date on new shards while the existing shards continue to
serve live read and write traffic. When you're ready to switch over,
the migration occurs with only a few seconds of read-only downtime.
During that time, existing data can be read, but new data cannot be
written.

The table below lists the sharding (or resharding) processes that you
would typically perform for different types of requirements:

Requirement | Action
----------- | ------
Uniformly increase read capacity | Add replicas or split shards
Uniformly increase write capacity | Split shards
Reclaim overprovisioned resources | Merge shards and/or keyspaces
Increase geo-diversity | Add new cells and replicas
Cool a hot tablet | For read access, add replicas or split shards. For write access, split shards.

### Filtered Replication

The cornerstone of resharding is replicating the right data. Vitess
implements the following functions to support filtered replication,
the process that ensures that the correct source tablet data is
transferred to the proper destination tablets. Since MySQL does not
support any filtering, this functionality is all specific to Vitess.

1. The source tablet tags transactions with comments so that MySQL binlogs
    contain the filtering data needed during the resharding process. The
    comments describe the scope of each transaction (its keyspace ID,
    table, etc.).
1. A server process uses the comments to filter the MySQL binlogs and
    stream the correct data to the destination tablet.
1. A client process on the destination tablet applies the filtered logs,
    which are just regular SQL statements at this point.

### Additional Tools and Processes

Vitess provides the following tools to help manage range-based shards:

* The [vtctl](/reference/vtctl.html) command-line tool supports
    functions for managing keyspaces, shards, tablets, and more.
* Client APIs account for sharding operations.
* The [MapReduce framework](https://github.com/youtube/vitess/blob/master/java/vtgate-client/src/main/java/com/youtube/vitess/vtgate/hadoop/README.md)
    fully utilizes key ranges to read data as quickly as possible,
    concurrently from all shards and all replicas.

## Custom Sharding

If your application already supports sharding or if you want to control
exactly which shard handles each query, Vitess can support your custom
sharding scheme. In that use case, each keyspace has a collection of
shards, and the client code always specifies the shard to which it is
directing a query.

One example of a custom sharding scheme is lookup-based sharding. In
lookup-based sharding, one keyspace is used as a lookup keyspace, and
it contains the mapping between a record's identifying key and the name
of the record's shard. To execute a query, the client first checks the
lookup table to locate the correct shard name and then routes the query
to that shard.

In a custom sharding scheme, shards can use any name you choose, and
they are always addressed by name. The vtgate API calls to use are
<code>ExecuteShard</code>, <code>ExecuteBatchShard</code>, and
<code>StreamExecuteShard</code>. None of the API calls for
**KeyspaceIds**, **KeyRanges**, or **EntityIds** are compatible with
a custom sharding scheme. Vitess' tools and processes for automated
resharding also do not support custom sharding schemes.

If you use a custom sharding scheme, you can still use the
[MapReduce framework](https://github.com/youtube/vitess/blob/master/java/vtgate-client/src/main/java/com/youtube/vitess/vtgate/hadoop/README.md)
to iterate over the data on multiple shards.
