use serde::{Deserialize, Deserializer, Serialize, Serializer};

/// A field explicitly present on the wire, including a nullable value.
pub fn present<'de, T: Deserialize<'de>, D: Deserializer<'de>>(
    deserializer: D,
) -> Result<Option<T>, D::Error> {
    T::deserialize(deserializer).map(Some)
}

macro_rules! wire_integer {
    ($name:ident, $primitive:ty) => {
        #[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
        pub struct $name(pub $primitive);

        impl Serialize for $name {
            fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
                serializer.collect_str(&self.0)
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
                let encoded = String::deserialize(deserializer)?;
                let parsed: $primitive = encoded.parse().map_err(serde::de::Error::custom)?;
                if encoded != parsed.to_string() {
                    return Err(serde::de::Error::custom("non-canonical integer"));
                }
                Ok(Self(parsed))
            }
        }
    };
}

wire_integer!(Int64, i64);
wire_integer!(UInt64, u64);

macro_rules! wire_string {
    ($name:ident) => {
        #[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
        #[serde(transparent)]
        pub struct $name(pub String);
    };
}

wire_string!(Decimal);
wire_string!(Timestamp);
wire_string!(Duration);
wire_string!(ContractBytes);
