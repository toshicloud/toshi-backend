module go.toshicloud.io/backend

go 1.26.0

require go.toshicloud.io/product v0.1.0

// The product module is not served at its import path yet; the backends'
// registries take its stability levels, so the module builds against the
// checkout's copy.
replace go.toshicloud.io/product => ../product
