extends SceneTree

const Renderer = preload("renderer.gd")
const PROTOCOL := 1
const MAX_REQUEST := 65536
var peer := StreamPeerTCP.new()
var renderer := Renderer.new()
var scene: Node
var width := 144
var height := 40
var input_bytes := PackedByteArray()
var world_size: Vector2
var pressed_codes: Array[int] = []

func _initialize() -> void:
	var args := OS.get_cmdline_user_args()
	if args.size() != 2:
		printerr("Termcade runtime requires its host connection.")
		quit(2)
		return
	var err := peer.connect_to_host("127.0.0.1", int(args[0]))
	if err != OK:
		quit(2)
		return
	while peer.get_status() == StreamPeerTCP.STATUS_CONNECTING:
		peer.poll()
		OS.delay_usec(1000)
	world_size = Vector2(ProjectSettings.get_setting("display/window/size/viewport_width", 640), ProjectSettings.get_setting("display/window/size/viewport_height", 360))
	_send({"protocol": PROTOCOL, "token": args[1], "title": ProjectSettings.get_setting("application/config/name", "Godot game")})

func _process(_delta: float) -> bool:
	# Host-driven fixed-FPS engine iterations. While the arcade is paused or
	# waiting on input, Godot cannot run ahead and accumulate invisible frames.
	while peer.get_status() == StreamPeerTCP.STATUS_CONNECTED:
		peer.poll()
		var available := peer.get_available_bytes()
		if available > 0:
			var result := peer.get_data(mini(available, MAX_REQUEST))
			if result[0] != OK:
				return true
			input_bytes.append_array(result[1])
			if input_bytes.size() > MAX_REQUEST:
				return true
			var newline := input_bytes.find(10)
			if newline >= 0:
				var request = JSON.parse_string(input_bytes.slice(0, newline).get_string_from_utf8())
				input_bytes = input_bytes.slice(newline + 1)
				if not request is Dictionary:
					return true
				return _request(request)
		OS.delay_usec(1000)
	return true

func _request(request: Dictionary) -> bool:
	if request.get("op") == "close":
		return true
	if request.get("op") == "reset":
		width = int(request.get("width", 144))
		height = int(request.get("height", 40))
		if width < 1 or width > 600 or height < 1 or height > 360:
			_send({"error": "invalid framebuffer dimensions"})
			return true
		for code in pressed_codes:
			var release := InputEventKey.new()
			release.keycode = code
			release.physical_keycode = code
			Input.parse_input_event(release)
		pressed_codes.clear()
		Input.flush_buffered_events()
		if scene != null:
			scene.free()
		var packed := load(ProjectSettings.get_setting("application/run/main_scene", "")) as PackedScene
		if packed == null:
			_send({"error": "cannot load main scene"})
			return true
		scene = packed.instantiate()
		root.add_child(scene)
		current_scene = scene
	elif request.get("op") != "step" or scene == null:
		_send({"error": "expected reset or step"})
		return true
	for key in request.get("keys", []):
		var event := InputEventKey.new()
		event.keycode = int(key.code)
		event.physical_keycode = event.keycode
		event.pressed = bool(key.down)
		event.echo = event.pressed and pressed_codes.has(event.keycode)
		if event.pressed and not pressed_codes.has(event.keycode):
			pressed_codes.append(event.keycode)
		elif not event.pressed:
			pressed_codes.erase(event.keycode)
		Input.parse_input_event(event)
	Input.flush_buffered_events()
	_send_frame.call_deferred()
	return false

func _send_frame() -> void:
	var failures := Renderer.validate(scene)
	if not failures.is_empty():
		_send({"error": "; ".join(failures)})
		quit(2)
		return
	var frame: Image = renderer.render(scene, width, height, world_size)
	_send({"width": width, "height": height, "pixels": Marshalls.raw_to_base64(frame.get_data())})

func _send(value: Dictionary) -> void:
	peer.put_data((JSON.stringify(value) + "\n").to_utf8_buffer())
