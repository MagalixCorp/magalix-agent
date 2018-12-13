GENERATED = packets_kinds.go

generate:
	@echo ":: generating $(GENERATED)"
	@echo "package proto" > $(GENERATED)
	@grep -Po 'type Packet\K\w+' packets.go \
		| grep -v 'Item$$' \
		| xargs -n1 -I{} bash -c " \
		echo '' >> $(GENERATED); \
		echo 'func (Packet{}) Kind() PacketKind {' >> $(GENERATED); \
		echo '	return PacketKind{}' >> $(GENERATED); \
		echo '}' >> $(GENERATED); \
		"
