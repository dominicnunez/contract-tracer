module example.com/externalonly

go 1.27.0

require example.com/replacement v0.0.0
replace example.com/replacement => ../replacement
