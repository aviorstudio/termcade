extends Node2D

var velocity := Vector2(24, 28)

func _physics_process(delta: float) -> void:
	$Paddle.position.x = clampf($Paddle.position.x + Input.get_axis("ui_left", "ui_right") * 80.0 * delta, 14, 130)
	$Ball.position += velocity * delta
	if $Ball.position.x < 3 or $Ball.position.x > 141:
		velocity.x = -velocity.x
	if $Ball.position.y < 3:
		velocity.y = absf(velocity.y)
	if velocity.y > 0 and $Ball.position.y >= 66 and $Ball.position.y <= 72 and absf($Ball.position.x - $Paddle.position.x) < 14:
		velocity.y = -absf(velocity.y)
	if $Ball.position.y > 82:
		$Ball.position = Vector2(72, 40)
		velocity = Vector2(24, 28)
