extends ColorRect
func _ready() -> void:
	if State.activations != 1:
		push_error("Autoload state did not survive the scene transition")
